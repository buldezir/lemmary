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

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/embed"
	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/inflight"
)

// Bounds one backfill tick. 0 disables the cron entirely, for an operator who
// wants embeddings for new uploads without paying to embed a whole archive on
// the next restart. It does not disable the manual sweep from Maintenance.
const EnvEmbeddingBackfillBatch = "EMBEDDING_BACKFILL_BATCH"

const defaultBackfillBatch = 20

// Stops a tick before the next one is due: a tick that never ends never logs
// what it did.
const backfillBudget = 50 * time.Second

// A sweep runs batch after batch, so on a large archive it has to stop
// somewhere; the next sweep resumes, since progress is recorded per document.
const sweepBudget = 30 * time.Minute

// An unreadable value falls back to the default rather than to zero: silently
// turning the feature off is the worse failure, since nothing would look wrong.
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

// Backfiller embeds documents no job will ever run for again: the archive that
// predates the model binding, a restored backup, a model or dimension switch, a
// chunker version bump, a soft-failed embed step, a stale edit.
//
// It runs from a cron tick and from a sweep the admin starts in Maintenance.
// They share one mutex, so the two never embed the same candidate twice.
type Backfiller struct {
	app   core.App
	rt    *config.Runtime
	batch int

	running sync.Mutex
	// The mutex cannot answer "is a sweep in progress?" without taking it, and
	// the Maintenance page asks on every poll.
	sweeping atomic.Bool
}

// Constructed in wiring rather than Register: the API routes are bound before
// the worker is, and both sides need the same instance for the locks to mean
// anything.
func NewBackfiller(app core.App, rt *config.Runtime) *Backfiller {
	return &Backfiller{app: app, rt: rt, batch: BackfillBatchFromEnv(app.Logger())}
}

func registerEmbeddingBackfill(app core.App, b *Backfiller) {
	if b == nil {
		return
	}
	if b.batch == 0 {
		app.Logger().Info("embedding backfill cron disabled; the Maintenance sweep still runs",
			"env", EnvEmbeddingBackfillBatch)
		return
	}

	cronExpr := config.WorkerCronFromEnv()
	app.Cron().MustAdd("embedding_backfill", cronExpr, func() {
		// In-flight for the same reason the job drain is: with encryption at
		// rest, a tick writing while the archive is sealed loses its work.
		defer inflight.Begin()()
		b.tick()
	})
	app.Logger().Info("embedding backfill registered", "cron", cronExpr, "batch", b.batch)
}

type batchResult struct {
	// Zero means the backlog is empty, which is what ends a sweep.
	Candidates int
	Embedded   int
	Failed     int
	Skipped    int
	Tokens     int
}

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

func (b *Backfiller) tick() {
	// A tick that overruns must not be joined by the next one, which would pick
	// the same candidates and pay twice. A sweep holds the same mutex.
	if !b.running.TryLock() {
		return
	}
	defer b.running.Unlock()

	// Before anything touches the app: this is the one path a half-wired
	// Backfiller can reach.
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

// Reports whether this call is what started the sweep. It returns before any
// embedding happens, because the caller is an HTTP request; progress is read
// back through Stats, which counts rows rather than tracking the goroutine.
func (b *Backfiller) StartSweep() bool {
	if !b.sweeping.CompareAndSwap(false, true) {
		return false
	}
	// Counted like the cron tick, for the same shutdown reason.
	done := inflight.Begin()
	go func() {
		defer done()
		defer b.sweeping.Store(false)
		b.sweep()
	}()
	return true
}

// A cron tick is not a sweep: it is over in under a minute and nothing waits.
func (b *Backfiller) SweepRunning() bool { return b.sweeping.Load() }

func (b *Backfiller) sweep() sweepSummary {
	// Lock, not TryLock: a sweep is an explicit click, so it waits out a cron
	// tick rather than dropping the request.
	b.running.Lock()
	defer b.running.Unlock()

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
			// Unbound mid-sweep: an empty backlog rather than an error, since
			// there is nothing left this sweep can do.
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

// EMBEDDING_BACKFILL_BATCH=0 turns the cron off, not the sweep, so a zero falls
// back to the default here.
func (b *Backfiller) sweepBatch() int {
	if b.batch <= 0 {
		return defaultBackfillBatch
	}
	return b.batch
}

// Drives batches until the backlog is empty, the deadline passes, or a batch
// fails. The clock and the batch are arguments so the stopping rules can be
// tested without a database.
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
		// A batch that neither embedded nor failed did not move the backlog,
		// so the next would return the same candidates forever. It happens
		// when the candidate query and the freshness check disagree.
		if res.Embedded == 0 && res.Failed == 0 {
			return summary, nil
		}
	}
}

// Callers hold b.running.
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
			continue
		}
		docCtx := aiprovider.WithDocumentRecord(ctx, document)
		result, err := embed.EmbedDocument(docCtx, b.app, snap.Embedder, document, false, logger)
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

// Swept before every batch run rather than only on delete, so rows left by a
// deletion made while the feature was off do not sit in the index forever.
func (b *Backfiller) sweepOrphans(logger *slog.Logger) {
	if swept, err := embedstore.DeleteOrphans(b.app.DB()); err != nil {
		logger.Warn("orphan sweep failed", slog.Any("error", err))
	} else if len(swept) > 0 {
		logger.Info("removed embeddings for deleted documents", "documents", len(swept))
	}
}

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
