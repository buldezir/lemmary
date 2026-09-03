package appapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chat"
	"lemmary/backend/internal/config"
)

// chatMaxBodyBytes caps a chat request. Both routes used to carry the whole
// transcript and so inherited PocketBase's 32MB route default; now that the
// body is one message plus a session id, there is no reason for it to be large.
const chatMaxBodyBytes = 64 << 10

const tooManySessionsMessage = "You have reached the maximum number of saved chats. Delete some to start a new one."

// conversationSession resolves the id that both the provider's cache key and
// the chat_sessions record will carry.
//
// A first turn has no session id yet: chat.AppendTurn mints one, and only after
// the provider has already answered. Minting it here instead means turn one and
// turn two name the same conversation, so the prompt prefix turn one warmed is
// still there for turn two.
func conversationSession(sessionID string) string {
	if id := strings.TrimSpace(sessionID); id != "" {
		return id
	}
	return core.GenerateDefaultRandomId()
}

type chatRequest struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
}

type chatResponse struct {
	Session *chat.SessionInfo `json:"session"`
	Message chat.MessageInfo  `json:"message"`
	Saved   bool              `json:"saved"`
}

// validateChatContent normalizes and bounds an incoming message.
//
// Rejected rather than truncated, unlike the stored assistant reply: sending
// the model half a question and answering it confidently is worse than saying
// the message is too long.
func validateChatContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", errors.New("A message is required.")
	}
	if utf8.RuneCountInString(content) > chat.MaxUserContentRunes {
		return "", fmt.Errorf("A message may be at most %d characters.", chat.MaxUserContentRunes)
	}
	return content, nil
}

// parseSearchMode reads the mode field. Research is the only mode worth naming:
// anything else -- including a legacy "shallow" or "deep" from an older client
// -- is plain search, which is also the cheaper of the two to get wrong.
func parseSearchMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), chat.ModeResearch) {
		return chat.ModeResearch
	}
	return chat.ModeSearch
}

// loadChatHistory returns the prior turns of an existing session, along with
// the session itself. Both are nil for a new conversation.
//
// A session of the wrong kind, or one attached to a different document, is
// reported as missing rather than forbidden: 404 for every mismatch means a
// document session's id cannot be probed through the search endpoint, and the
// document check in particular stops a conversation started against document A
// from being continued against B's OCR text under A's title.
//
// The session comes back so a caller can check what only it knows about --
// deep search uses it for the mode the conversation is already in.
func loadChatHistory(app core.App, ownerID, sessionID string, kind chat.Kind, documentID string) (*core.Record, []ai.ChatMessage, error) {
	if sessionID == "" {
		return nil, nil, nil
	}
	session, err := chat.FindOwnedSession(app, ownerID, sessionID)
	if err != nil {
		return nil, nil, err
	}
	if session.GetString("kind") != string(kind) {
		return nil, nil, chat.ErrNotFound
	}
	if documentID != "" && session.GetString("document") != documentID {
		return nil, nil, chat.ErrNotFound
	}
	history, err := chat.History(app, session.Id)
	if err != nil {
		return nil, nil, err
	}
	return session, history, nil
}

// writeChatSessionError maps a session lookup failure onto a response.
func writeChatSessionError(e *core.RequestEvent, app core.App, err error) error {
	if errors.Is(err, chat.ErrNotFound) {
		return writeError(e, http.StatusNotFound, "Chat not found.")
	}
	app.Logger().Error("chat session lookup failed", slog.Any("error", err))
	return writeError(e, http.StatusInternalServerError, "Chat is unavailable.")
}

// unsavedMessage renders a reply that was produced but could not be stored.
func unsavedMessage(role, content string, hits []ai.DocumentHit) chat.MessageInfo {
	return chat.MessageInfo{Role: role, Content: content, Documents: hits}
}

// latestAssistantMessage returns the just-written assistant turn, so the client
// gets its real record id and stored content.
//
// Falls back to an id-less view rather than failing the request: the turn is
// already committed, and re-reading it is a convenience.
func latestAssistantMessage(app core.App, sessionID, reply string, hits []ai.DocumentHit) chat.MessageInfo {
	records, err := chat.ListMessages(app, sessionID, chat.MaxReplayMessages)
	if err == nil && len(records) > 0 {
		last := records[len(records)-1]
		if last.GetString("role") == chat.RoleAssistant {
			return chat.ToMessageInfo(last)
		}
	}
	return unsavedMessage(chat.RoleAssistant, reply, hits)
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
		// collection rules. This answers document access; session ownership is
		// resolved separately below.
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
		content, err := validateChatContent(req.Content)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		chatter := rt.Snapshot().Chatter
		if chatter == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI chat is not configured; update Settings.")
		}

		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		_, history, err := loadChatHistory(app, ownerID, req.SessionID, chat.KindDocument, documentID)
		if err != nil {
			return writeChatSessionError(e, app, err)
		}
		messages := append(history, ai.ChatMessage{Role: chat.RoleUser, Content: content})

		conversationID := conversationSession(req.SessionID)

		// Request context: closing the tab cancels the upstream LLM call.
		reply, err := chatter.Chat(aiprovider.WithSession(e.Request.Context(), conversationID), ocrText, messages)
		if err != nil {
			app.Logger().Error("document chat failed", "document", documentID, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "The AI provider could not complete the request.")
		}

		session, err := chat.AppendTurn(app, req.SessionID, chat.NewSession{
			ID:         conversationID,
			UserID:     ownerID,
			Kind:       chat.KindDocument,
			DocumentID: documentID,
		}, chat.Turn{
			UserContent:      content,
			AssistantContent: reply,
		})
		if err != nil {
			if errors.Is(err, chat.ErrTooManySessions) {
				return writeError(e, http.StatusConflict, tooManySessionsMessage)
			}
			app.Logger().Error("document chat persist failed", "document", documentID, slog.Any("error", err))
			return writeJSON(e, http.StatusOK, chatResponse{
				Message: unsavedMessage(chat.RoleAssistant, reply, nil),
				Saved:   false,
			})
		}

		info := chat.ToSessionInfo(session)
		return writeJSON(e, http.StatusOK, chatResponse{
			Session: &info,
			Message: latestAssistantMessage(app, session.Id, reply, nil),
			Saved:   true,
		})
	}
}
