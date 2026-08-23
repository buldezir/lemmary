package appapi

import (
	"log/slog"
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/taxonomy"
)

func handlePostTaxonomyPrune(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		result, err := taxonomy.PruneOrphans(app)
		if err != nil {
			app.Logger().Error("taxonomy prune failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Stale data cleanup failed.")
		}
		app.Logger().Info(
			"taxonomy prune finished",
			slog.Int("tags", result.Tags),
			slog.Int("correspondents", result.Correspondents),
			slog.Int("document_types", result.DocumentTypes),
		)
		return writeJSON(e, http.StatusOK, result)
	}
}
