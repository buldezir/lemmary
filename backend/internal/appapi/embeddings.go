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

// EmbeddingSweeper is worker.Backfiller, narrowed so this package does not
// depend on the worker's internals.
type EmbeddingSweeper interface {
	// StartSweep reports whether this call started the sweep; false means one
	// was already running.
	StartSweep() bool
	SweepRunning() bool
}

// noEmbeddingModelMessage points at the one place the binding can be made, so
// an admin on a maintenance page need not guess where to go.
const noEmbeddingModelMessage = "No embedding model is bound. Choose one in Settings before embedding the archive."

// embeddingBackfillResponse is shared by the start and status routes, so the
// page renders one shape either way. Stats counts rows, which survives a
// restart in a way a goroutine's own counter would not.
type embeddingBackfillResponse struct {
	Started bool             `json:"started"`
	Running bool             `json:"running"`
	Stats   embedstore.Stats `json:"stats"`
}

// A model that is not bound reports an empty backlog rather than an error:
// nothing is wrong, there is just nothing to count.
func loadEmbeddingStats(app core.App, cfg config.Config) (embedstore.Stats, error) {
	model := ""
	if config.HasEmbedding(cfg) {
		model = cfg.EmbeddingModel
	}
	return embedstore.LoadStats(app.DB(), model, cfg.EmbeddingDims, chunk.Version, time.Now())
}

// handlePostEmbeddingBackfill answers immediately rather than waiting for the
// sweep, which over an existing archive runs far longer than a request should
// hold a connection open. The Management page polls the status route.
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

// handleGetEmbeddingBackfill is the poll behind "Embedding N of M".
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
