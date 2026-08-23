package appapi

import (
	"log/slog"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
)

func handlePostDuplicatesScan(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		result, err := duplicates.ScanAll(app, rt.Snapshot().Cfg)
		if err != nil {
			app.Logger().Error("duplicate scan failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Duplicate scan failed.")
		}
		return writeJSON(e, http.StatusOK, result)
	}
}
