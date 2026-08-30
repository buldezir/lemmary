package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chat"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/fulltext"
)

// maxAvailableTagNames caps how many tag names are inlined into the agent prompt.
const maxAvailableTagNames = 500

type searchRequest struct {
	// SessionID continues an existing conversation; empty starts a new one.
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Mode      string `json:"mode"`
}

type searchResponse struct {
	// Session is null when Saved is false -- see the AppendTurn failure path.
	Session   *chat.SessionInfo `json:"session"`
	Message   chat.MessageInfo  `json:"message"`
	Documents []ai.DocumentHit  `json:"documents"`
	Saved     bool              `json:"saved"`
}

func handleDeepSearch(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req searchRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		content, err := validateChatContent(req.Content)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		mode := parseSearchMode(req.Mode)

		agent := rt.Snapshot().SearchAgent
		if agent == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI search is not configured; update Settings.")
		}

		// Two different questions, deliberately answered differently.
		//
		// ownerID is whose sidebar this conversation belongs in, so a superuser
		// session resolves to its paired users record. searchUserID is whose
		// documents the search may see, and there a superuser stays unscoped --
		// matching the homepage listing and the PocketBase collection rules.
		// Collapsing them would either hide an admin's own chats or scope an
		// admin's search to one account.
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		searchUserID := ""
		if !e.HasSuperuserAuth() {
			searchUserID = e.Auth.Id
		}

		history, err := loadChatHistory(app, ownerID, req.SessionID, chat.KindSearch, "")
		if err != nil {
			return writeChatSessionError(e, app, err)
		}
		messages := append(history, ai.ChatMessage{Role: chat.RoleUser, Content: content})

		availableTags, err := listAvailableTagNames(app, searchUserID)
		if err != nil {
			app.Logger().Error("deep search list tags failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Search is unavailable.")
		}

		searcher := func(ctx context.Context, args ai.SearchDocumentsArgs) ([]ai.DocumentHit, error) {
			return searchUserDocuments(app, idx, searchUserID, args)
		}

		// Use the request context so closing the browser tab cancels the agent
		// loop instead of leaving several LLM round-trips running.
		reply, hits, err := agent.Search(e.Request.Context(), messages, mode, availableTags, searcher)
		if err != nil {
			app.Logger().Error("deep search failed", slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "The AI provider could not complete the search.")
		}
		if hits == nil {
			hits = []ai.DocumentHit{}
		}

		session, err := chat.AppendTurn(app, req.SessionID, chat.NewSession{
			UserID: ownerID,
			Kind:   chat.KindSearch,
		}, chat.Turn{
			UserContent:      content,
			AssistantContent: reply,
			Documents:        hits,
			Mode:             string(mode),
		})
		if err != nil {
			if errors.Is(err, chat.ErrTooManySessions) {
				return writeError(e, http.StatusConflict, tooManySessionsMessage)
			}
			// The provider already answered. Hand the reply over unsaved rather
			// than discarding work the user paid for; the client says so.
			app.Logger().Error("deep search persist failed", slog.Any("error", err))
			return writeJSON(e, http.StatusOK, searchResponse{
				Message:   unsavedMessage(chat.RoleAssistant, reply, hits),
				Documents: hits,
				Saved:     false,
			})
		}

		info := chat.ToSessionInfo(session)
		return writeJSON(e, http.StatusOK, searchResponse{
			Session:   &info,
			Message:   latestAssistantMessage(app, session.Id, reply, hits),
			Documents: hits,
			Saved:     true,
		})
	}
}

// listAvailableTagNames returns the tag names offered to the search agent.
// userID scopes the list to that owner; empty lists every tag (superusers).
func listAvailableTagNames(app core.App, userID string) ([]string, error) {
	filter := ""
	var params []dbx.Params
	if userID != "" {
		filter = "user = {:userId}"
		params = append(params, dbx.Params{"userId": userID})
	}
	records, err := app.FindRecordsByFilter("tags", filter, "name", maxAvailableTagNames, 0, params...)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	names := make([]string, 0, len(records))
	for _, record := range records {
		if name := strings.TrimSpace(record.GetString("name")); name != "" {
			names = append(names, name)
		}
	}
	return names, nil
}
