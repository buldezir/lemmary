package appapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/reprocess"
)

type reprocessFailedRequest struct {
	Limit       int      `json:"limit"`
	Mode        string   `json:"mode"`
	DocumentIDs []string `json:"document_ids"`
}

func handlePostReprocessFailed(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req reprocessFailedRequest
		// Every field is optional, so an empty body is a valid "one default
		// batch" request.
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}

		mode, err := reprocess.ParseMode(req.Mode)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		result, err := reprocess.RunBatch(app, reprocess.Request{
			OwnerUserID: ownerID,
			DocumentIDs: req.DocumentIDs,
			Limit:       req.Limit,
			Mode:        mode,
		})
		if err != nil {
			app.Logger().Error("reprocess batch failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Reprocess failed.")
		}
		return writeJSON(e, http.StatusOK, result)
	}
}
