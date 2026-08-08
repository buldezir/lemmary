package appapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/duplicates"
)

func handlePostDuplicatesScan(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		result, err := duplicates.ScanAll(app, rt.Snapshot().Cfg)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Duplicate scan failed: "+err.Error())
		}
		return writeJSON(e, http.StatusOK, result)
	}
}
