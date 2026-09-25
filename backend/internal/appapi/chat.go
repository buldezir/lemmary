package appapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chat"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/websearch"
)

// chatMaxBodyBytes caps a chat request: the body is one message plus a session
// id, where PocketBase's route default is 32MB. Sized above what the content
// column can hold, so the question is what the model does with a long message
// rather than whether it may be sent.
const chatMaxBodyBytes = 2 << 20

const tooManySessionsMessage = "You have reached the maximum number of saved chats. Delete some to start a new one."

type chatRequest struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	// RunID correlates this request with the stored assistant message, so a
	// caller that lost the response recovers this answer rather than a
	// different answer to identical text.
	RunID string `json:"run_id"`
	// The provider and model to open the conversation on, instead of the chat
	// binding in Settings. Read only when SessionID is empty.
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	// Web lets this turn reach the public web. Per turn rather than stored with
	// the conversation: unlike the binding, nothing in the transcript depends on
	// it, and a metered tool is better defaulted off on every reload.
	Web bool `json:"web"`
}

// conversationBinding pins an existing conversation to the binding stored with
// it: the transcript was produced by that model. Ignored rather than refused,
// unlike a mode mismatch, because the client is echoing what it loaded with the
// session and a stale echo is not worth failing a question over.
func conversationBinding(session *core.Record, requested aiprovider.Binding) aiprovider.Binding {
	if session != nil {
		return chat.BindingOf(session)
	}
	return requested.Normalized()
}

// recordedBinding spells the configured pair out when no override was chosen,
// so that changing the chat model later cannot move an old conversation onto it
// mid-transcript. Both halves or neither: aiprovider.Resolve refuses a provider
// with no model, and nothing stored means "follow Settings".
func recordedBinding(requested aiprovider.Binding, providerID, model string) aiprovider.Binding {
	// Against the zero value, not Binding.Empty: Empty is keyed on the provider
	// id, so a model with no provider would read as no override and quietly run
	// the configured one. Resolve refuses that pair, but only if it sees it.
	if requested.Normalized() != (aiprovider.Binding{}) {
		return requested.Normalized()
	}
	configured := aiprovider.Binding{ProviderID: providerID, Model: model}.Normalized()
	if configured.Empty() || configured.Model == "" {
		return aiprovider.Binding{}
	}
	return configured
}

// conversationSnapshot treats an unusable binding this request picked as a bad
// request: answering on another model is what the picker exists to prevent. One
// the conversation carries falls back to Settings instead, since the provider
// row can be deleted long after the transcript, and a chat naming a gone
// provider beats one that cannot be continued.
func conversationSnapshot(
	app core.App,
	rt *config.Runtime,
	o config.Overrides,
	session *core.Record,
	requested aiprovider.Binding,
) (config.Snapshot, error) {
	snap, err := rt.WithOverrides(app, o)
	if err == nil {
		return snap, nil
	}
	if session == nil && requested.Normalized() != (aiprovider.Binding{}) {
		return snap, err
	}
	app.Logger().Warn("conversation binding no longer resolves; continuing on the configured model",
		slog.Any("error", err))
	return rt.Snapshot(), nil
}

type chatResponse struct {
	Session *chat.SessionInfo `json:"session"`
	Message chat.MessageInfo  `json:"message"`
	Saved   bool              `json:"saved"`
}

// Rejected rather than truncated, unlike the stored assistant reply: answering
// half a question confidently is worse than saying the message is too long.
func validateChatContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", errors.New("A message is required.")
	}
	return content, nil
}

// validateRunID rejects what the column cannot represent rather than silently
// making recovery ambiguous.
func validateRunID(raw string) (string, error) {
	runID := strings.TrimSpace(raw)
	if utf8.RuneCountInString(runID) > chat.MaxRunIDRunes {
		return "", fmt.Errorf("A run id may be at most %d characters.", chat.MaxRunIDRunes)
	}
	return runID, nil
}

// parseSearchMode treats anything but research as plain search, which is the
// cheaper of the two to get wrong.
func parseSearchMode(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), chat.ModeResearch) {
		return chat.ModeResearch
	}
	return chat.ModeSearch
}

// loadChatHistory returns the prior turns and the session, both nil for a new
// conversation. A session of the wrong kind, or one attached to another
// document, is reported as missing rather than forbidden: that way an id cannot
// be probed across endpoints, and a conversation started against document A
// cannot be continued against B's OCR text under A's title.
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

// discardEmptySession drops a session opened for a turn that never landed. Best
// effort: the answer, or the error being reported, is what the caller owes.
func discardEmptySession(app core.App, session *core.Record) {
	if session == nil {
		return
	}
	if err := chat.DiscardEmptySession(app, session); err != nil {
		app.Logger().Error("discard empty chat session failed", "session", session.Id, slog.Any("error", err))
	}
}

func writeChatSessionError(e *core.RequestEvent, app core.App, err error) error {
	if errors.Is(err, chat.ErrNotFound) {
		return writeError(e, http.StatusNotFound, "Chat not found.")
	}
	app.Logger().Error("chat session lookup failed", slog.Any("error", err))
	return writeError(e, http.StatusInternalServerError, "Chat is unavailable.")
}

// unsavedMessage renders a reply that was produced but could not be stored.
func unsavedMessage(content string, hits []ai.DocumentHit) chat.MessageInfo {
	return chat.MessageInfo{Role: chat.RoleAssistant, Content: content, Documents: hits}
}

// latestAssistantMessage falls back to an id-less view rather than failing the
// request: the turn is already committed, and re-reading it is a convenience.
func latestAssistantMessage(app core.App, sessionID, runID, reply string, hits []ai.DocumentHit) chat.MessageInfo {
	records, err := chat.ListMessages(app, sessionID, chat.MaxReplayMessages)
	if err == nil {
		// Through the same fold a reload goes through, so the answer the client
		// is handed now carries the trail it will still have after a refresh. A
		// research transcript is also full of assistant rows that are tool
		// calls, and the fold is what keeps one of those from being mistaken
		// for the answer.
		messages := chat.VisibleMessages(records)
		for _, info := range slices.Backward(messages) {
			if info.Role == chat.RoleAssistant && (runID == "" || info.RunID == runID) {
				return info
			}
		}
	}
	return unsavedMessage(reply, hits)
}

// loadChatDocument finds the document and the OCR text the chat is about. On
// failure it writes the response itself and reports handled.
func loadChatDocument(app core.App, e *core.RequestEvent) (*core.Record, string, bool, error) {
	documentID := strings.TrimSpace(e.Request.PathValue("documentId"))
	if documentID == "" {
		return nil, "", true, writeError(e, http.StatusBadRequest, "Document id is required.")
	}

	document, err := app.FindRecordById("documents", documentID)
	if err != nil {
		return nil, "", true, writeError(e, http.StatusNotFound, "Document not found.")
	}
	// Superusers bypass ownership, matching the PocketBase collection rules.
	// This answers document access; session ownership is resolved later.
	if !e.HasSuperuserAuth() && !CanReadDocument(app, document, e.Auth.Id) {
		return nil, "", true, writeError(e, http.StatusForbidden, "You do not have access to this document.")
	}

	ocrText := strings.TrimSpace(document.GetString("ocr_text"))
	if ocrText == "" {
		return nil, "", true, writeError(e, http.StatusBadRequest, "Document has no OCR text yet.")
	}
	return document, ocrText, false, nil
}

// The error is the message the 400 carries.
func decodeChatRequest(e *core.RequestEvent) (chatRequest, string, string, error) {
	var req chatRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return req, "", "", errors.New("Invalid request body.")
	}
	content, err := validateChatContent(req.Content)
	if err != nil {
		return req, "", "", err
	}
	runID, err := validateRunID(req.RunID)
	if err != nil {
		return req, "", "", err
	}
	return req, content, runID, nil
}

func handleDocumentChat(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		document, ocrText, handled, err := loadChatDocument(app, e)
		if handled {
			return err
		}
		documentID := document.Id

		req, content, requestID, err := decodeChatRequest(e)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		session, messages, err := loadChatHistory(app, ownerID, req.SessionID, chat.KindDocument, documentID)
		if err != nil {
			return writeChatSessionError(e, app, err)
		}
		messages = append(messages, ai.ChatMessage{Role: chat.RoleUser, Content: content})

		// After the session is loaded, because a continued conversation's stored
		// binding is what decides, not the request's.
		cfg := rt.Snapshot().Cfg
		requested := aiprovider.Binding{ProviderID: req.ProviderID, Model: req.Model}
		binding := conversationBinding(session, recordedBinding(requested, cfg.ExtractProviderID, cfg.ExtractModel))
		snap, err := conversationSnapshot(app, rt, config.Overrides{Chat: binding}, session, requested)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		chatter := snap.Chatter
		if chatter == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI chat is not configured; update Settings.")
		}

		// The conversation exists before the question is asked, so the cache key
		// given to the provider is the id the chat keeps. opened holds the record
		// only when this request created it.
		var opened *core.Record
		if session == nil {
			session, err = chat.CreateSession(app, chat.NewSession{
				UserID:       ownerID,
				Kind:         chat.KindDocument,
				DocumentID:   documentID,
				Binding:      binding,
				FirstMessage: content,
			})
			if err != nil {
				if errors.Is(err, chat.ErrTooManySessions) {
					return writeError(e, http.StatusConflict, tooManySessionsMessage)
				}
				app.Logger().Error("document chat session create failed", "document", documentID, slog.Any("error", err))
				return writeError(e, http.StatusInternalServerError, "Chat is unavailable.")
			}
			opened = session
		}

		// Detached from the connection like a search run: the turn is stored
		// whether or not this response can be delivered, and a client that lost
		// its connection comes back for it. No run id, because this surface has
		// no Cancel button; the budget ends a run nobody is waiting for.
		runCtx, stopRun := startDetachedRun(e.Request.Context(), ownerID, "", session.Id)
		defer stopRun()

		chatCtx := aiprovider.WithDocumentRecord(runCtx, document)
		// Nil unless both sides agreed: an operator bound a provider, and the
		// user asked for the web on this turn.
		var web *websearch.Tavily
		if req.Web {
			web = snap.WebSearch
		}
		reply, err := chatter.Chat(aiprovider.WithSession(chatCtx, session.Id), ocrText, messages, web)
		if err != nil {
			discardEmptySession(app, opened)
			return writeRunError(runCtx, e, app.Logger().With("document", documentID), "document chat", err)
		}

		session, err = chat.AppendTurn(app, ownerID, session.Id, chat.Turn{
			UserContent:      content,
			AssistantContent: reply,
			RunID:            requestID,
		})
		if err != nil {
			app.Logger().Error("document chat persist failed", "document", documentID, slog.Any("error", err))
			discardEmptySession(app, opened)
			return writeJSON(e, http.StatusOK, chatResponse{
				Message: unsavedMessage(reply, nil),
				Saved:   false,
			})
		}

		info := chat.ToSessionInfo(session)
		return writeJSON(e, http.StatusOK, chatResponse{
			Session: &info,
			Message: latestAssistantMessage(app, session.Id, requestID, reply, nil),
			Saved:   true,
		})
	}
}
