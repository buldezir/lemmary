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

type chatRequest struct {
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	// The provider and model to open the conversation on, instead of the chat
	// binding in Settings. Read only when SessionID is empty: see
	// conversationBinding.
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
}

// conversationBinding decides which provider and model a turn runs on.
//
// An existing conversation runs on the one stored with it, and what the request
// carries is ignored. The transcript replayed below was produced by that model,
// and answering the next question with another one reads that work back as if
// it were its own -- the same argument the search mode conflict makes a few
// lines further down in search.go.
//
// Ignored rather than refused, unlike a mode mismatch. There the client is
// choosing, and a disagreement means the two have drifted; here it is echoing
// the binding it loaded with the session, and a stale echo is not a mistake
// worth failing a question over.
func conversationBinding(session *core.Record, requested aiprovider.Binding) aiprovider.Binding {
	if session != nil {
		return chat.BindingOf(session)
	}
	return requested.Normalized()
}

// recordedBinding is the binding a conversation is opened with: the override
// when one was chosen, and otherwise the configured pair spelled out.
//
// Spelling it out is the point. A conversation that stored nothing followed
// Settings for the rest of its life, so changing the chat model moved every
// old conversation onto the new one -- mid-transcript, with the answers above
// produced by a model that was no longer answering. That is the same
// inconsistency the pinning rule exists to prevent; it was simply invisible
// because the binding was never written down.
//
// Both halves or neither: a provider with no model is refused by
// aiprovider.Resolve, so stamping half a pair on a part-configured instance
// would turn a chat that used to work into a 400. Nothing stored means "follow
// Settings", which is what those instances did before.
func recordedBinding(requested aiprovider.Binding, providerID, model string) aiprovider.Binding {
	// Against the zero value, not Binding.Empty, for the same reason
	// config.Overrides.Empty is: Empty is keyed on the provider id, so a model
	// with no provider would read as no override and quietly run the configured
	// one. Resolve refuses that pair, and can only do so if it sees it.
	if requested.Normalized() != (aiprovider.Binding{}) {
		return requested.Normalized()
	}
	configured := aiprovider.Binding{ProviderID: providerID, Model: model}.Normalized()
	if configured.Empty() || configured.Model == "" {
		return aiprovider.Binding{}
	}
	return configured
}

// conversationSnapshot turns the binding a turn runs on into the clients that
// run it.
//
// An unusable binding this request picked is a bad request: answering on a
// different model is the substitution the picker exists to prevent. One the
// conversation carries falls back to Settings instead -- the provider row can
// be deleted long after the transcript, which is why chat_sessions.provider is
// text and not a relation, and refusing it would leave every pinned chat unable
// to take another turn once its provider is rotated out. The stale pair stays
// on the record, so the picker still names the gone provider: a chat naming the
// wrong model beats one that cannot be continued.
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

// discardEmptySession drops a session opened for a turn that never landed, so
// a failed or abandoned first question does not leave an empty chat in the
// sidebar. Best effort: the answer, or the error being reported, is the thing
// the caller owes the user.
func discardEmptySession(app core.App, session *core.Record) {
	if session == nil {
		return
	}
	if err := chat.DiscardEmptySession(app, session); err != nil {
		app.Logger().Error("discard empty chat session failed", "session", session.Id, slog.Any("error", err))
	}
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

		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		session, history, err := loadChatHistory(app, ownerID, req.SessionID, chat.KindDocument, documentID)
		if err != nil {
			return writeChatSessionError(e, app, err)
		}
		messages := append(history, ai.ChatMessage{Role: chat.RoleUser, Content: content})

		// Resolved after the session is loaded, because a continued
		// conversation's stored binding is what decides, not the request's.
		cfg := rt.Snapshot().Cfg
		requested := aiprovider.Binding{ProviderID: req.ProviderID, Model: req.Model}
		binding := conversationBinding(session, recordedBinding(requested, cfg.ChatProviderID, cfg.ChatModel))
		snap, err := conversationSnapshot(app, rt, config.Overrides{Chat: binding}, session, requested)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		chatter := snap.Chatter
		if chatter == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI chat is not configured; update Settings.")
		}

		// The conversation exists before the question is asked, so the id the
		// provider is given as a cache key is the id the chat keeps. opened
		// holds the record only when this request is what created it: a
		// conversation that was already there is never this request's to
		// take back.
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

		// Request context: closing the tab cancels the upstream LLM call.
		reply, err := chatter.Chat(aiprovider.WithSession(e.Request.Context(), session.Id), ocrText, messages)
		if err != nil {
			app.Logger().Error("document chat failed", "document", documentID, slog.Any("error", err))
			discardEmptySession(app, opened)
			return writeError(e, http.StatusBadGateway, "The AI provider could not complete the request.")
		}

		session, err = chat.AppendTurn(app, ownerID, session.Id, chat.Turn{
			UserContent:      content,
			AssistantContent: reply,
		})
		if err != nil {
			app.Logger().Error("document chat persist failed", "document", documentID, slog.Any("error", err))
			discardEmptySession(app, opened)
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
