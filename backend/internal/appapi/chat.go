package appapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
)

type chatRequest struct {
	Messages []ai.ChatMessage `json:"messages"`
}

type chatResponse struct {
	Message ai.ChatMessage `json:"message"`
}

func handleDocumentChat(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		documentID := strings.TrimSpace(e.Request.PathValue("documentId"))
		if documentID == "" {
			return writeError(e, http.StatusBadRequest, "Document id is required.")
		}

		document, err := app.FindRecordById("documents", documentID)
		if err != nil {
			return writeError(e, http.StatusNotFound, "Document not found.")
		}
		// Superusers bypass ownership, matching deep search and the PocketBase
		// collection rules.
		if !e.HasSuperuserAuth() && document.GetString("user") != e.Auth.Id {
			return writeError(e, http.StatusForbidden, "You do not have access to this document.")
		}

		ocrText := strings.TrimSpace(document.GetString("ocr_text"))
		if ocrText == "" {
			return writeError(e, http.StatusBadRequest, "Document has no OCR text yet.")
		}

		var req chatRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if len(req.Messages) == 0 {
			return writeError(e, http.StatusBadRequest, "At least one message is required.")
		}

		chatter := rt.Snapshot().Chatter
		if chatter == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI chat is not configured; update Settings.")
		}

		// Request context: closing the tab cancels the upstream LLM call.
		reply, err := chatter.Chat(e.Request.Context(), ocrText, req.Messages)
		if err != nil {
			app.Logger().Error("document chat failed", "document", documentID, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "The AI provider could not complete the request.")
		}

		return writeJSON(e, http.StatusOK, chatResponse{
			Message: ai.ChatMessage{
				Role:    "assistant",
				Content: reply,
			},
		})
	}
}
