package worker

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/embed"
	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/inflight"
)

// EnvEmbeddingBackfillBatch bounds one backfill tick. 0 disables the cron
// entirely, which is the escape hatch for an operator who wants embeddings for
// new uploads without paying to embed an archive of a hundred thousand
// documents on the next restart. It does not disable the manual sweep from
// Management: that one is a deliberate click, not a schedule.
const EnvEmbeddingBackfillBatch = "EMBEDDING_BACKFILL_BATCH"

const defaultBackfillBatch = 20

// backfillBudget stops a tick before the next one is due. The cron default is
// one minute, and two overlapping ticks would only fight over the same
// candidates -- TryLock already prevents that, but a tick that never ends would
// also never log what it did.
const backfillBudget = 50 * time.Second

// sweepBudget bounds a manual sweep. A sweep runs batch after batch until the
// backlog is drained, so on a large archive with a slow provider it has to stop
// somewhere: the admin can click again, and the next sweep resumes where this
// one left off because progress is recorded per document.
const sweepBudget = 30 * time.Minute

// BackfillBatchFromEnv reads the per-tick document budget. A value that cannot
// be read falls back to the default rather than to zero: silently turning the
// feature off is the worse of the two failures, because nothing about the
// archive would look wrong.
func BackfillBatchFromEnv(logger *slog.Logger) int {
	raw := strings.TrimSpace(os.Getenv(EnvEmbeddingBackfillBatch))
	if raw == "" {
		return defaultBackfillBatch
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < 0 {
		if logger != nil {
			logger.Error("unreadable "+EnvEmbeddingBackfillBatch+"; using the default",
				"value", raw, "default", defaultBackfillBatch)
		}
		return defaultBackfillBatch
	}
	return parsed
}

// Backfiller embeds documents the pipeline never reached.
//
// It is what makes the feature usable on a real archive rather than only on
// uploads made after it was switched on. The cases it covers are all the ones
// where no job will ever run again for a document that needs embedding: the
// existing archive when a model is first bound, a restored backup (whose
// documents skip job creation), a model or dimension switch, a chunker version
// bump, a soft-failed embed step, and an edit that only marked a document stale.
//
// It runs from two places: a cron tick that takes one batch a minute, and a
// sweep the admin starts from Management that keeps taking batches until the
// backlog is gone. They share one mutex, so the two never embed the same
// candidate twice.
type Backfiller struct {
	app   core.App
	rt    *config.Runtime
	batch int

	// running serializes the cron tick against a manual sweep.
	running sync.Mutex
	// sweeping is the manual sweep's own flag. The mutex cannot answer "is a
	// sweep in progress?" without taking it, and the Management page has to ask
	// that on every poll.
	sweeping atomic.Bool
}

// NewBackfiller builds the sweeper. It is constructed in wiring rather than in
// Register because the API routes are bound before the worker is, and both
// sides need the same instance for their locks to mean anything.
func NewBackfiller(app core.App, rt *config.Runtime) *Backfiller {
	return &Backfiller{app: app, rt: rt, batch: BackfillBatchFromEnv(app.Logger())}
}

func registerEmbeddingBackfill(app core.App, b *Backfiller) {
	if b == nil {
		return
	}
	if b.batch == 0 {
		app.Logger().Info("embedding backfill cron disabled; the Management sweep still runs",
			"env", EnvEmbeddingBackfillBatch)
		return
	}

	cronExpr := config.WorkerCronFromEnv()
	app.Cron().MustAdd("embedding_backfill", cronExpr, func() {
		// Counted as in-flight work for the same reason the job drain is: with
		// encryption at rest on, a tick still writing while the archive is
		// sealed loses everything it had done.
		defer inflight.Begin()()
		b.tick()
	})
	app.Logger().Info("embedding backfill registered", "cron", cronExpr, "batch", b.batch)
}

// batchResult reports one pass over a candidate batch.
type batchResult struct {
	// Candidates is how many documents the batch query returned. Zero means the
	// backlog is empty, which is what ends a sweep.
	Candidates int
	Embedded   int
	Failed     int
	Skipped    int
	Tokens     int
}

// sweepSummary totals the batches one sweep ran.
type sweepSummary struct {
	Batches  int
	Embedded int
	Failed   int
	Skipped  int
	Tokens   int
}

func (s *sweepSummary) add(res batchResult) {
	s.Batches++
	s.Embedded += res.Embedded
	s.Failed += res.Failed
	s.Skipped += res.Skipped
	s.Tokens += res.Tokens
}

// tick embeds one batch. It is the cron entry point.
func (b *Backfiller) tick() {
	// A tick that overruns its budget must not be joined by the next one; the
	// second would pick the same candidates and pay for them twice. The manual
	// sweep holds the same mutex, so a tick during a sweep does nothing.
	if !b.running.TryLock() {
		return
	}
	defer b.running.Unlock()

	// Before anything touches the app: with no model bound there is nothing to
	// do, and this is the one path a half-wired Backfiller can reach.
	snap := b.rt.Snapshot()
	if snap.Embedder == nil {
		return
	}
	logger := b.app.Logger().With("component", "embed_backfill")
	b.sweepOrphans(logger)

	ctx, cancel := context.WithTimeout(context.Background(), backfillBudget)
	defer cancel()

	res, err := b.embedBatch(ctx, snap, b.batch, logger)
	if err != nil {
		logger.Error("listing embedding candidates failed", slog.Any("error", err))
		return
	}
	if res.Embedded == 0 && res.Failed == 0 {
		return
	}
	logger.Info("embedding backfill tick",
		"embedded", res.Embedded,
		"failed", res.Failed,
		"prompt_tokens", res.Tokens,
		"remaining", b.remaining(snap),
	)
}

// StartSweep runs the whole backlog in the background and reports whether this
// call is what started it.
//
// It returns before any embedding happens: a sweep over a large archive takes
// minutes, and the caller is an HTTP request that must not hold a connection
// open for it. Progress is read back through Stats, which counts rows rather
// than tracking the goroutine.
func (b *Backfiller) StartSweep() bool {
	if !b.sweeping.CompareAndSwap(false, true) {
		return false
	}
	// Counted like the cron tick: a sweep still writing while an encrypted
	// archive is sealed on shutdown would lose everything it had done.
	done := inflight.Begin()
	go func() {
		defer done()
		defer b.sweeping.Store(false)
		b.sweep()
	}()
	return true
}

// SweepRunning reports whether a manual sweep is in progress. A cron tick is
// not a sweep: it is over in under a minute and nothing waits on it.
func (b *Backfiller) SweepRunning() bool { return b.sweeping.Load() }

// sweep takes batch after batch until the backlog is drained.
func (b *Backfiller) sweep() sweepSummary {
	// Lock, not TryLock: a sweep is an explicit click, so it waits out a cron
	// tick (at most backfillBudget) rather than dropping the request. Ticks
	// arriving while the sweep holds this do the dropping instead.
	b.running.Lock()
	defer b.running.Unlock()

	// Checked before the app is touched, exactly as in tick.
	if b.rt.Snapshot().Embedder == nil {
		return sweepSummary{}
	}
	logger := b.app.Logger().With("component", "embed_sweep")
	b.sweepOrphans(logger)

	ctx, cancel := context.WithTimeout(context.Background(), sweepBudget)
	defer cancel()

	started := time.Now()
	summary, err := sweepLoop(time.Now, started.Add(sweepBudget), func() (batchResult, error) {
		snap := b.rt.Snapshot()
		if snap.Embedder == nil {
			// The model was unbound mid-sweep. Reported as an empty backlog
			// rather than an error: there is nothing left this sweep can do.
			return batchResult{}, nil
		}
		return b.embedBatch(ctx, snap, b.sweepBatch(), logger)
	})
	if err != nil {
		logger.Error("embedding sweep failed", slog.Any("error", err))
	}
	logger.Info("embedding sweep finished",
		"batches", summary.Batches,
		"embedded", summary.Embedded,
		"failed", summary.Failed,
		"skipped", summary.Skipped,
		"prompt_tokens", summary.Tokens,
		"seconds", int(time.Since(started).Seconds()),
		"remaining", b.remaining(b.rt.Snapshot()),
	)
	return summary
}

// sweepBatch is how many documents one sweep batch takes. EMBEDDING_BACKFILL_BATCH=0
// turns the cron off, not the sweep, so a zero falls back to the default here.
func (b *Backfiller) sweepBatch() int {
	if b.batch <= 0 {
		return defaultBackfillBatch
	}
	return b.batch
}

// sweepLoop drives batches until the backlog is empty, the deadline passes, or
// a batch fails.
//
// It takes the clock and the batch as arguments so the loop's stopping rules
// can be tested without a database behind them: the interesting behaviour is
// when it stops, not what one batch embeds.
func sweepLoop(now func() time.Time, deadline time.Time, batch func() (batchResult, error)) (sweepSummary, error) {
	var summary sweepSummary
	for {
		if !now().Before(deadline) {
			return summary, nil
		}
		res, err := batch()
		if err != nil {
			return summary, err
		}
		summary.add(res)
		if res.Candidates == 0 {
			return summary, nil
		}
		// A batch that neither embedded nor failed anything did not move the
		// backlog, so the next one would return the same candidates forever.
		// It happens when the candidate query and the freshness check disagree,
		// or when every candidate has been deleted since the query ran.
		if res.Embedded == 0 && res.Failed == 0 {
			return summary, nil
		}
	}
}

// embedBatch embeds up to batch candidates. Callers hold b.running.
func (b *Backfiller) embedBatch(
	ctx context.Context,
	snap config.Snapshot,
	batch int,
	logger *slog.Logger,
) (batchResult, error) {
	model := snap.Embedder.Model()
	dims := snap.Cfg.EmbeddingDims
	ids, err := embedstore.Candidates(b.app.DB(), model, dims, chunk.Version, batch, time.Now())
	if err != nil {
		return batchResult{}, err
	}

	res := batchResult{Candidates: len(ids)}
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		document, err := b.app.FindRecordById("documents", id)
		if err != nil {
			// The orphan sweep will clean up after it; nothing to do here.
			continue
		}
		result, err := embed.EmbedDocument(ctx, b.app, snap.Embedder, document, false, logger)
		if err != nil {
			res.Failed++
			logger.Warn("backfill embedding failed", "document", id, slog.Any("error", err))
			continue
		}
		if result.Skipped {
			res.Skipped++
			continue
		}
		res.Embedded++
		res.Tokens += result.PromptTokens
		if err := config.RecordEmbeddingDims(b.app, result.Dims); err != nil {
			logger.Warn("recording embedding dimensions failed", slog.Any("error", err))
		}
	}
	return res, nil
}

// sweepOrphans clears rows whose document is gone.
//
// Swept before every batch run rather than only on delete, so rows left behind
// by a deletion that happened while the feature was off, or by a restored
// backup, do not sit in the index forever.
func (b *Backfiller) sweepOrphans(logger *slog.Logger) {
	if swept, err := embedstore.DeleteOrphans(b.app.DB()); err != nil {
		logger.Warn("orphan sweep failed", slog.Any("error", err))
	} else if len(swept) > 0 {
		logger.Info("removed embeddings for deleted documents", "documents", len(swept))
	}
}

// remaining is the backlog size for a log line, or 0 when it cannot be read.
func (b *Backfiller) remaining(snap config.Snapshot) int {
	if snap.Embedder == nil {
		return 0
	}
	stats, err := embedstore.LoadStats(
		b.app.DB(), snap.Embedder.Model(), snap.Cfg.EmbeddingDims, chunk.Version, time.Now())
	if err != nil {
		return 0
	}
	return stats.Pending
}
