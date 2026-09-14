package appapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/reprocess"
)

type reprocessFailedRequest struct {
	Limit       int      `json:"limit"`
	Mode        string   `json:"mode"`
	DocumentIDs []string `json:"document_ids"`
	// Overrides are the provider and model to run this batch on, instead of
	// Settings. Only ocr, extract and embedding are read, the three bindings
	// the pipeline uses; a chat or search override is refused rather than
	// stored and never looked at.
	Overrides config.Overrides `json:"overrides"`
}

func handlePostReprocessFailed(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req reprocessFailedRequest
		// Every field is optional, so an empty body is a valid request.
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}

		mode, err := reprocess.ParseMode(req.Mode)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		// Refused rather than dropped, so a client sending a binding the
		// pipeline cannot use learns that instead of watching the batch run
		// on the configured model.
		if !req.Overrides.Chat.Empty() || !req.Overrides.Search.Empty() {
			return writeError(e, http.StatusBadRequest, "A reprocess job can only override the ocr, extract and embedding bindings.")
		}
		// Validated up front as well as in the create hook: the hook refuses
		// one job, so a bad provider id would otherwise surface only after the
		// rest of the batch was queued.
		if err := req.Overrides.Validate(app, rt.Snapshot().Cfg); err != nil {
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
			Overrides:   req.Overrides,
		})
		if err != nil {
			app.Logger().Error("reprocess batch failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Reprocess failed.")
		}
		return writeJSON(e, http.StatusOK, result)
	}
}
