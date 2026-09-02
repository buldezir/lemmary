package appapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/embedstore"
)

// EmbeddingSweeper runs the embedding backfill on demand. worker.Backfiller is
// the implementation; the interface keeps the API package from depending on the
// worker's internals and lets a handler test drive the two states that matter.
type EmbeddingSweeper interface {
	// StartSweep begins a background sweep and reports whether this call is
	// what started it. False means one was already running.
	StartSweep() bool
	// SweepRunning reports whether a sweep is in progress.
	SweepRunning() bool
}

// noEmbeddingModelMessage points at the one place the binding can be made. The
// backfill has nothing to embed with until it is, and an admin reading "not
// configured" on a maintenance page has no reason to guess where to go.
const noEmbeddingModelMessage = "No embedding model is bound. Choose one in Settings before embedding the archive."

// embeddingBackfillResponse is what both the start and the status route answer
// with, so the page has one shape to render whether it just clicked or is
// polling. Stats is the progress: "embedded of total" counts rows, which
// survives a restart in a way a goroutine's own counter would not.
type embeddingBackfillResponse struct {
	Started bool             `json:"started"`
	Running bool             `json:"running"`
	Stats   embedstore.Stats `json:"stats"`
}

// loadEmbeddingStats scans the backlog for the configured binding. A model that
// is not bound reports a disabled, empty backlog rather than an error: nothing
// is wrong, there is just nothing to count.
func loadEmbeddingStats(app core.App, cfg config.Config) (embedstore.Stats, error) {
	model := ""
	if config.HasEmbedding(cfg) {
		model = cfg.EmbeddingModel
	}
	return embedstore.LoadStats(app.DB(), model, cfg.EmbeddingDims, chunk.Version, time.Now())
}

// handlePostEmbeddingBackfill starts a sweep over every document that still
// needs embedding.
//
// It answers immediately rather than waiting for the sweep: a first run over an
// existing archive takes as long as the provider does, which is far longer than
// any request should hold a connection open. The Management page polls the
// status route for progress.
func handlePostEmbeddingBackfill(app core.App, rt *config.Runtime, sweeper EmbeddingSweeper) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		cfg := rt.Snapshot().Cfg
		if !config.HasEmbedding(cfg) {
			return writeError(e, http.StatusConflict, noEmbeddingModelMessage)
		}
		if sweeper == nil {
			app.Logger().Error("embedding backfill requested with no sweeper wired")
			return writeError(e, http.StatusInternalServerError, "Embedding backfill is unavailable.")
		}

		started := sweeper.StartSweep()
		stats, err := loadEmbeddingStats(app, cfg)
		if err != nil {
			app.Logger().Error("embedding stats failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to load embedding statistics.")
		}
		return writeJSON(e, http.StatusOK, embeddingBackfillResponse{
			Started: started,
			Running: sweeper.SweepRunning(),
			Stats:   stats,
		})
	}
}

// handleGetEmbeddingBackfill reports whether a sweep is running, with the
// backlog as it stands. It is the poll behind "Embedding N of M".
func handleGetEmbeddingBackfill(app core.App, rt *config.Runtime, sweeper EmbeddingSweeper) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		stats, err := loadEmbeddingStats(app, rt.Snapshot().Cfg)
		if err != nil {
			app.Logger().Error("embedding stats failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to load embedding statistics.")
		}
		return writeJSON(e, http.StatusOK, embeddingBackfillResponse{
			Running: sweeper != nil && sweeper.SweepRunning(),
			Stats:   stats,
		})
	}
}
