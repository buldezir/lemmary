package worker

import (
	"context"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
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
// documents on the next restart.
const EnvEmbeddingBackfillBatch = "EMBEDDING_BACKFILL_BATCH"

const defaultBackfillBatch = 20

// backfillBudget stops a tick before the next one is due. The cron default is
// one minute, and two overlapping ticks would only fight over the same
// candidates -- TryLock already prevents that, but a tick that never ends would
// also never log what it did.
const backfillBudget = 50 * time.Second

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

// backfiller embeds documents the pipeline never reached.
//
// It is what makes the feature usable on a real archive rather than only on
// uploads made after it was switched on. The cases it covers are all the ones
// where no job will ever run again for a document that needs embedding: the
// existing archive when a model is first bound, a restored backup (whose
// documents skip job creation), a model or dimension switch, a chunker version
// bump, a soft-failed embed step, and an edit that only marked a document stale.
type backfiller struct {
	app     core.App
	rt      *config.Runtime
	batch   int
	running sync.Mutex
}

func registerEmbeddingBackfill(app core.App, rt *config.Runtime) {
	batch := BackfillBatchFromEnv(app.Logger())
	if batch == 0 {
		app.Logger().Info("embedding backfill disabled", "env", EnvEmbeddingBackfillBatch)
		return
	}

	b := &backfiller{app: app, rt: rt, batch: batch}
	cronExpr := config.WorkerCronFromEnv()
	app.Cron().MustAdd("embedding_backfill", cronExpr, func() {
		// Counted as in-flight work for the same reason the job drain is: with
		// encryption at rest on, a tick still writing while the archive is
		// sealed loses everything it had done.
		defer inflight.Begin()()
		b.tick()
	})
	app.Logger().Info("embedding backfill registered", "cron", cronExpr, "batch", batch)
}

func (b *backfiller) tick() {
	// A tick that overruns its budget must not be joined by the next one; the
	// second would pick the same candidates and pay for them twice.
	if !b.running.TryLock() {
		return
	}
	defer b.running.Unlock()

	snap := b.rt.Snapshot()
	if snap.Embedder == nil {
		return
	}
	logger := b.app.Logger().With("component", "embed_backfill")

	// Swept every tick rather than only on delete, so rows left behind by a
	// deletion that happened while the feature was off, or by a restored
	// backup, do not sit in the index forever.
	if swept, err := embedstore.DeleteOrphans(b.app.DB()); err != nil {
		logger.Warn("orphan sweep failed", slog.Any("error", err))
	} else if len(swept) > 0 {
		logger.Info("removed embeddings for deleted documents", "documents", len(swept))
	}

	model := snap.Embedder.Model()
	dims := snap.Cfg.EmbeddingDims
	ids, err := embedstore.Candidates(b.app.DB(), model, dims, chunk.Version, b.batch, time.Now())
	if err != nil {
		logger.Error("listing embedding candidates failed", slog.Any("error", err))
		return
	}
	if len(ids) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), backfillBudget)
	defer cancel()
	deadline := time.Now().Add(backfillBudget)

	embedded, failed, tokens := 0, 0, 0
	for _, id := range ids {
		if time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		document, err := b.app.FindRecordById("documents", id)
		if err != nil {
			// The orphan sweep will clean up after it; nothing to do here.
			continue
		}
		result, err := embed.EmbedDocument(ctx, b.app, snap.Embedder, document, false, logger)
		if err != nil {
			failed++
			logger.Warn("backfill embedding failed", "document", id, slog.Any("error", err))
			continue
		}
		if result.Skipped {
			continue
		}
		embedded++
		tokens += result.PromptTokens
		if err := config.RecordEmbeddingDims(b.app, result.Dims); err != nil {
			logger.Warn("recording embedding dimensions failed", slog.Any("error", err))
		}
	}

	if embedded == 0 && failed == 0 {
		return
	}
	remaining := 0
	if stats, err := embedstore.LoadStats(b.app.DB(), model, dims, chunk.Version, time.Now()); err == nil {
		remaining = stats.Pending
	}
	logger.Info("embedding backfill tick",
		"embedded", embedded,
		"failed", failed,
		"prompt_tokens", tokens,
		"remaining", remaining,
	)
}
