package appapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

const translateTimeout = 10 * time.Minute

// clearStaleTranslation drops the stored translation whenever the text it was
// made from changes, whether by a user's correction or by a re-run of OCR.
func clearStaleTranslation(e *core.RecordEvent) error {
	if e.Record.GetString("ocr_text") != e.Record.Original().GetString("ocr_text") {
		e.Record.Set("ocr_text_translated", "")
	}
	return e.Next()
}

// handleDocumentTranslation returns the OCR text in the result language,
// translating and storing it first when nothing is stored or ?force=1 asks.
func handleDocumentTranslation(app core.App, snapshot func() config.Snapshot) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		documentID := strings.TrimSpace(e.Request.PathValue("documentId"))
		document, err := app.FindRecordById("documents", documentID)
		if err != nil {
			return writeError(e, http.StatusNotFound, "Document not found.")
		}
		if !e.HasSuperuserAuth() && !CanReadDocument(app, document, e.Auth.Id) {
			return writeError(e, http.StatusForbidden, "You do not have access to this document.")
		}
		ocrText := document.GetString("ocr_text")
		if strings.TrimSpace(ocrText) == "" {
			return writeError(e, http.StatusBadRequest, "Document has no OCR text yet.")
		}

		force := e.Request.URL.Query().Get("force") == "1"
		if stored := document.GetString("ocr_text_translated"); stored != "" && !force {
			return writeJSON(e, http.StatusOK, map[string]string{"text": stored})
		}

		snap := snapshot()
		if snap.Cfg.ProcessingResultLanguage == "" {
			return writeError(e, http.StatusBadRequest, "No result language is set; update Settings.")
		}
		if snap.AI == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI is not configured; update Settings.")
		}

		// Detached so a reader who leaves the page still finds the result stored
		// on their next visit.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(e.Request.Context()), translateTimeout)
		defer cancel()
		translated, err := snap.AI.Translate(aiprovider.WithDocumentRecord(ctx, document), ocrText)
		if errors.Is(err, ai.ErrTranslationTruncated) {
			app.Logger().Warn("ocr translation truncated", "document", documentID, slog.Any("error", err))
			return writeError(e, http.StatusUnprocessableEntity, "The model stopped before the end of the document; it is too long to translate in one reply of this model.")
		}
		if err != nil {
			app.Logger().Error("ocr translation failed", "document", documentID, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "Translation failed.")
		}

		// One conditional column write rather than app.Save: Save writes the
		// whole row, which would undo a correction committed while the model was
		// answering. No hooks or realtime event are wanted for a cache column.
		res, err := app.NonconcurrentDB().NewQuery(
			"UPDATE documents SET ocr_text_translated = {:translated} WHERE id = {:id} AND ocr_text = {:source}",
		).Bind(dbx.Params{"translated": translated, "id": documentID, "source": ocrText}).Execute()
		if err != nil {
			app.Logger().Error("ocr translation save failed", "document", documentID, slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Could not save the translation.")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return writeError(e, http.StatusConflict, "The OCR text changed while it was being translated; try again.")
		}
		return writeJSON(e, http.StatusOK, map[string]string{"text": translated})
	}
}
