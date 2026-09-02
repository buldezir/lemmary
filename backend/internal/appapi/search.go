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
	// Set when a research answer was cut off mid-generation; see
	// ai.ResearchResult.Incomplete. Not stored with the turn: it describes this
	// generation, not the text, and a reopened chat has no way to redo it.
	Incomplete bool `json:"incomplete,omitempty"`
	// Why the turn could not be saved, when Saved is false.
	Detail string `json:"detail,omitempty"`
}

// searchTurn is everything a search turn needs resolved before the provider is
// called: whose conversation it is, what the model is shown, and what it may
// search.
type searchTurn struct {
	agent     ai.SearchAgent
	sessionID string
	ownerID   string
	content   string
	mode      string
	messages  []ai.ChatMessage
	tools     agentTools
}

func (t searchTurn) research() bool { return t.mode == chat.ModeResearch }

// agentTools resolves the per-request scoping shared by both modes: the tag
// catalogue offered to the agent, and the searcher/reader closures bound to the
// caller's own documents.
type agentTools struct {
	tags   []string
	search ai.DocumentSearcher
	read   ai.DocumentReader
}

func buildAgentTools(app core.App, idx *fulltext.Index, userID string) (agentTools, error) {
	tags, err := listAvailableTagNames(app, userID)
	if err != nil {
		return agentTools{}, err
	}
	return agentTools{
		tags: tags,
		search: func(ctx context.Context, args ai.SearchDocumentsArgs) (ai.SearchToolResult, error) {
			return searchUserDocuments(app, idx, userID, args)
		},
		read: func(ctx context.Context, ids []string) ([]ai.DocumentContent, error) {
			return readUserDocuments(app, userID, ids)
		},
	}, nil
}

// prepareSearchTurn does the work both search handlers share, from decoding the
// body to loading the conversation's history.
//
// On failure it writes the response itself and reports handled: the caller
// returns the error straight through. Both handlers call this before anything
// is streamed, so a failure here is still an ordinary HTTP error.
func prepareSearchTurn(app core.App, rt *config.Runtime, idx *fulltext.Index, e *core.RequestEvent) (searchTurn, bool, error) {
	var req searchRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return searchTurn{}, true, writeError(e, http.StatusBadRequest, "Invalid request body.")
	}
	content, err := validateChatContent(req.Content)
	if err != nil {
		return searchTurn{}, true, writeError(e, http.StatusBadRequest, err.Error())
	}

	agent := rt.Snapshot().SearchAgent
	if agent == nil {
		return searchTurn{}, true, writeError(e, http.StatusServiceUnavailable, "AI search is not configured; update Settings.")
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
		return searchTurn{}, true, writeOwnerError(e, err)
	}
	searchUserID := ""
	if !e.HasSuperuserAuth() {
		searchUserID = e.Auth.Id
	}

	session, history, err := loadChatHistory(app, ownerID, req.SessionID, chat.KindSearch, "")
	if err != nil {
		return searchTurn{}, true, writeChatSessionError(e, app, err)
	}

	// A conversation stays in the mode it started in, and this is where that
	// holds rather than in the page that hides the switch. The transcript
	// replayed below was produced by one mode, and answering the next question
	// under the other one reads that work back as if it were its own -- a
	// research transcript continued as a listing search, or the reverse, is a
	// different product answering from the wrong material. Refused rather than
	// silently corrected, because the client already knows which mode the chat
	// is in and sending the other one means the two have drifted.
	mode := parseSearchMode(req.Mode)
	if session != nil {
		if stored := session.GetString("mode"); stored != "" && stored != mode {
			return searchTurn{}, true, writeError(e, http.StatusConflict,
				"This chat is a "+stored+" chat and cannot change mode. Start a new chat to switch.")
		}
	}

	tools, err := buildAgentTools(app, idx, searchUserID)
	if err != nil {
		app.Logger().Error("search list tags failed", slog.Any("error", err))
		return searchTurn{}, true, writeError(e, http.StatusInternalServerError, "Search is unavailable.")
	}

	return searchTurn{
		agent:     agent,
		sessionID: req.SessionID,
		ownerID:   ownerID,
		content:   content,
		mode:      mode,
		messages:  append(history, ai.ChatMessage{Role: chat.RoleUser, Content: content}),
		tools:     tools,
	}, false, nil
}

// persistSearchTurn stores the exchange and renders what the client gets back.
//
// A storage failure is not allowed to swallow the answer: the provider has
// already been paid for it, so the reply is handed over unsaved and the
// conversation simply does not become resumable. The one failure passed back to
// the caller is ErrTooManySessions, which is the user's to act on.
func persistSearchTurn(app core.App, t searchTurn, reply string, hits []ai.DocumentHit) (searchResponse, error) {
	session, err := chat.AppendTurn(app, t.sessionID, chat.NewSession{
		UserID: t.ownerID,
		Kind:   chat.KindSearch,
	}, chat.Turn{
		UserContent:      t.content,
		AssistantContent: reply,
		Documents:        hits,
		Mode:             t.mode,
	})
	if err != nil {
		if errors.Is(err, chat.ErrTooManySessions) {
			return searchResponse{}, err
		}
		app.Logger().Error("search persist failed", slog.Any("error", err))
		return searchResponse{
			Message:   unsavedMessage(chat.RoleAssistant, reply, hits),
			Documents: hits,
			Saved:     false,
			Detail:    "This answer could not be saved, so the chat will not appear in your history.",
		}, nil
	}

	info := chat.ToSessionInfo(session)
	return searchResponse{
		Session:   &info,
		Message:   latestAssistantMessage(app, session.Id, reply, hits),
		Documents: hits,
		Saved:     true,
	}, nil
}

func handleDeepSearch(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		turn, handled, err := prepareSearchTurn(app, rt, idx, e)
		if handled {
			return err
		}

		// Use the request context so closing the browser tab cancels the agent
		// loop instead of leaving several LLM round-trips running.
		var reply string
		var hits []ai.DocumentHit
		incomplete := false
		if turn.research() {
			// Non-streaming fallback for clients that cannot read SSE.
			result, researchErr := turn.agent.Research(e.Request.Context(), ai.ResearchRequest{
				Messages:      turn.messages,
				AvailableTags: turn.tools.tags,
				Search:        turn.tools.search,
				Read:          turn.tools.read,
			}, nil)
			reply, hits, incomplete, err = result.Reply, result.Documents, result.Incomplete, researchErr
		} else {
			reply, hits, err = turn.agent.Search(e.Request.Context(), turn.messages, turn.tools.tags, turn.tools.search)
		}
		if err != nil {
			app.Logger().Error("deep search failed", "mode", turn.mode, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "The AI provider could not complete the search.")
		}
		if hits == nil {
			hits = []ai.DocumentHit{}
		}

		response, err := persistSearchTurn(app, turn, reply, hits)
		if err != nil {
			return writeError(e, http.StatusConflict, tooManySessionsMessage)
		}
		response.Incomplete = incomplete
		return writeJSON(e, http.StatusOK, response)
	}
}

// searchSavedEvent closes a research stream with the stored turn: the session
// the client needs for its URL and sidebar, and the message with its real
// record id. Saved is false when the answer was produced but could not be
// stored, and Detail then says why.
type searchSavedEvent struct {
	Type      string            `json:"type"`
	Session   *chat.SessionInfo `json:"session"`
	Message   chat.MessageInfo  `json:"message"`
	Documents []ai.DocumentHit  `json:"documents"`
	Saved     bool              `json:"saved"`
	Detail    string            `json:"detail,omitempty"`
}

// handleResearchStream runs a research turn and reports each step as it
// happens, then streams the answer. A research run can spend a minute searching
// and reading, which is far too long to show as a single spinner.
func handleResearchStream(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		turn, handled, err := prepareSearchTurn(app, rt, idx, e)
		if handled {
			return err
		}
		// This endpoint only ever researches, and research is the expensive
		// mode. Without this, a client that omitted the field -- or sent a
		// legacy "deep" -- would get a full research run out of what it thought
		// was a plain search.
		if !turn.research() {
			return writeError(e, http.StatusBadRequest, `This endpoint streams research; send mode "research" or use /api/app/search.`)
		}

		// Everything below is streamed, so errors are reported as events —
		// the status line has already been written by this point.
		stream := newSSEWriter(e)
		// Every model completion is a silent gap on this connection, and the
		// first one comes before any step event. Stopped before returning.
		stopHeartbeat := stream.Heartbeat(e.Request.Context())
		defer stopHeartbeat()

		result, err := turn.agent.Research(e.Request.Context(), ai.ResearchRequest{
			Messages:      turn.messages,
			AvailableTags: turn.tools.tags,
			Search:        turn.tools.search,
			Read:          turn.tools.read,
		}, func(event ai.ResearchEvent) { stream.Send(event) })
		if err != nil {
			if e.Request.Context().Err() != nil {
				// The client hung up; nothing left to report it to.
				app.Logger().Info("research cancelled by client")
				return nil
			}
			app.Logger().Error("research failed", slog.Any("error", err))
			stream.Send(ai.ResearchEvent{Type: "error", Message: "The AI provider could not complete the research."})
			stream.Send(ai.ResearchEvent{Type: "done"})
			return nil
		}

		documents := result.Documents
		if documents == nil {
			documents = []ai.DocumentHit{}
		}
		stream.Send(ai.ResearchEvent{Type: "documents", Documents: documents})
		// The whole answer follows the deltas: the deltas are a live preview,
		// this is the authoritative text (citation-checked). Incomplete says
		// whether it is the whole answer — a generation that outran the request
		// timeout is kept, not discarded, but the client has to be able to tell
		// the difference and say so.
		stream.Send(ai.ResearchEvent{
			Type:       "message",
			Content:    result.Reply,
			Incomplete: result.Incomplete,
		})

		// Stored only now, with the answer complete. Note the client is already
		// showing it: this event is what makes the conversation resumable, not
		// what makes it visible. Hitting the session cap cannot be a 409 here --
		// the status line went out with the first step event -- so it is
		// reported the same way as any other failure to store: the answer
		// stands, the chat is not saved, and Detail says why.
		saved, err := persistSearchTurn(app, turn, result.Reply, documents)
		if err != nil {
			saved = searchResponse{
				Message:   unsavedMessage(chat.RoleAssistant, result.Reply, documents),
				Documents: documents,
				Detail:    tooManySessionsMessage,
			}
		}
		stream.Send(searchSavedEvent{
			Type:      "saved",
			Session:   saved.Session,
			Message:   saved.Message,
			Documents: saved.Documents,
			Saved:     saved.Saved,
			Detail:    saved.Detail,
		})
		stream.Send(ai.ResearchEvent{Type: "done"})
		return nil
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
