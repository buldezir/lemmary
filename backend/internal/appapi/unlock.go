package appapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/i18n"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/pdftool"
	"lemmary/backend/internal/worker"
)

// handlePostDocumentUnlock takes the password of a password_required PDF once.
// It is never stored or logged: nothing here writes the request body anywhere.
func handlePostDocumentUnlock(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if req.Password == "" {
			return writeError(e, http.StatusBadRequest, "A password is required.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		documentID := strings.TrimSpace(e.Request.PathValue("documentId"))
		document, err := app.FindRecordById("documents", documentID)
		if err != nil {
			return writeError(e, http.StatusNotFound, "Document not found.")
		}
		// The owner only, admin included: a share grants reading, not replacing the file.
		if document.GetString("user") != ownerID {
			return writeError(e, http.StatusForbidden, "You do not have access to this document.")
		}

		err = worker.Unlock(e.Request.Context(), app, document, req.Password)
		if err == nil {
			return writeJSON(e, http.StatusOK, map[string]string{"status": document.GetString("processing_status")})
		}
		return writeUnlockError(app, e, documentID, err)
	}
}

func writeUnlockError(app core.App, e *core.RequestEvent, documentID string, err error) error {
	var dup *duplicates.ErrDuplicate
	switch {
	case errors.Is(err, pdftool.ErrIncorrectPassword):
		return writeError(e, http.StatusBadRequest, "Incorrect password.")
	case errors.Is(err, worker.ErrNotLocked):
		return writeError(e, http.StatusConflict, "This document is not waiting for a password.")
	case errors.As(err, &dup):
		return writeJSON(e, http.StatusBadRequest, map[string]any{
			"detail":       i18n.T(i18n.FromRequest(e.Request), "This document is already in your library."),
			"duplicate_of": dup.ExistingID,
		})
	}
	// The limits hook refuses the save with an ApiError carrying the limit.
	if apiErr, ok := errors.AsType[*router.ApiError](err); ok {
		if data, ok := apiErr.RawData().(map[string]any); ok {
			if exceeded, ok := data["limit"].(*limits.ErrExceeded); ok {
				return writeBadRequest(e, exceeded)
			}
		}
	}
	app.Logger().Error("unlocking a document failed", "document", documentID, slog.Any("error", err))
	return writeError(e, http.StatusInternalServerError, "Unlocking failed.")
}
