package appapi

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
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
		if err != nil {
			app.Logger().Error("ocr translation failed", "document", documentID, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "Translation failed.")
		}

		// Re-read rather than save the record loaded above: the owner may have
		// corrected the document while the model was answering.
		fresh, err := app.FindRecordById("documents", documentID)
		if err != nil {
			return writeError(e, http.StatusNotFound, "Document not found.")
		}
		if fresh.GetString("ocr_text") != ocrText {
			return writeError(e, http.StatusConflict, "The OCR text changed while it was being translated; try again.")
		}
		fresh.Set("ocr_text_translated", translated)
		if err := app.Save(fresh); err != nil {
			app.Logger().Error("ocr translation save failed", "document", documentID, slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Could not save the translation.")
		}
		return writeJSON(e, http.StatusOK, map[string]string{"text": translated})
	}
}
