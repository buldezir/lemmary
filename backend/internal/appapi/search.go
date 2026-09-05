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
	"lemmary/backend/internal/aiprovider"
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
	// RunID lets the client cancel this run explicitly. A run outlives its
	// connection now, so hanging up no longer stops it; a client that wants it
	// stopped generates an id here and POSTs it to /search/cancel. Optional:
	// omitting it costs the ability to cancel, nothing else.
	RunID string `json:"run_id"`
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
	agent ai.SearchAgent
	// session is the conversation this turn belongs to, opened before the
	// provider is called rather than after it answers. opened holds the same
	// record only when this request is what created it -- what may be taken
	// back when the turn never lands.
	session  *core.Record
	opened   *core.Record
	ownerID  string
	runID    string
	content  string
	mode     string
	messages []ai.ChatMessage
	tools    agentTools
	// priorDocuments are the hits earlier turns of this conversation found.
	// Research may read them by id without searching for them again.
	priorDocuments []ai.DocumentHit
}

func (t searchTurn) research() bool { return t.mode == chat.ModeResearch }

// agentContext names the conversation on the context the agent loop runs under,
// so every completion it makes -- rounds, the final answer, and the helper
// calls its tools fan out -- reaches the provider under one cache key.
func (t searchTurn) agentContext(parent context.Context) context.Context {
	return aiprovider.WithSession(parent, t.session.Id)
}

// agentTools resolves the per-request scoping shared by both modes: the tag
// catalogue offered to the agent, and the searcher/reader closures bound to the
// caller's own documents.
type agentTools struct {
	tags   []string
	search ai.DocumentSearcher
	read   ai.DocumentReader
	// survey and count are nil when the tools are unavailable: no helper
	// model for a survey, no database for a count.
	survey ai.DocumentSurveyor
	count  ai.DocumentCounter
	// dense is set when the retriever has an embedding leg; the prompt is
	// worded differently for a search that crosses languages by itself.
	dense bool
}

// buildAgentTools binds one retriever per request. Both closures share it, so
// per-turn work is done once rather than per tool call.
//
// The dense half is attached only when both ends of it exist: an embedding
// model to turn the question into a vector, and a chunk index to search with
// it. Either one missing leaves the retriever on keywords alone, which is the
// same code path an instance with no embedding provider has always run.
func buildAgentTools(app core.App, rt *config.Runtime, idx *fulltext.Index, userID string) (agentTools, error) {
	tags, err := listAvailableTagNames(app, userID)
	if err != nil {
		return agentTools{}, err
	}
	snap := rt.Snapshot()
	retriever := &agentRetriever{app: app, idx: idx, userID: userID, helper: snap.SearchHelper}
	if embedder := snap.Embedder; embedder != nil && idx != nil && idx.ChunksReady() {
		retriever.embedQuery = embedQueryFunc(embedder)
		retriever.chunks = idx
	}
	tools := agentTools{
		tags:   tags,
		search: retriever.search,
		read:   retriever.read,
		count:  retriever.count,
		dense:  retriever.embedQuery != nil,
	}
	if retriever.helper != nil {
		tools.survey = retriever.survey
	}
	return tools, nil
}

// embedQueryFunc adapts the embedder to the one vector the retriever wants.
// The production interface reports token usage and batches, neither of which
// the retriever has any use for.
func embedQueryFunc(embedder ai.Embedder) func(context.Context, string) ([]float32, error) {
	return func(ctx context.Context, text string) ([]float32, error) {
		result, err := embedder.Embed(ctx, []string{text})
		if err != nil {
			return nil, err
		}
		if len(result.Vectors) == 0 {
			return nil, fmt.Errorf("embedding the query returned no vector")
		}
		return result.Vectors[0], nil
	}
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

	tools, err := buildAgentTools(app, rt, idx, searchUserID)
	if err != nil {
		app.Logger().Error("search list tags failed", slog.Any("error", err))
		return searchTurn{}, true, writeError(e, http.StatusInternalServerError, "Search is unavailable.")
	}

	// A follow-up question is usually about what the last answer just cited,
	// and until now the run started with no memory of it at all: the model had
	// to guess a query that would rediscover a document it had already read.
	var priorDocuments []ai.DocumentHit
	if session != nil {
		priorDocuments, err = chat.PriorHits(app, session.Id)
		if err != nil {
			// Losing the carried evidence costs a search, not the answer.
			app.Logger().Warn("search prior hits failed", slog.Any("error", err))
		}
	}

	// Last, so a failure above cannot leave an empty conversation behind, and
	// before the provider, so the agent loop runs inside the session it will be
	// stored in. Hitting the cap is a plain 409 here; once the stream has
	// started there is no status line left to say so with.
	var opened *core.Record
	if session == nil {
		session, err = chat.CreateSession(app, chat.NewSession{
			UserID:       ownerID,
			Kind:         chat.KindSearch,
			Mode:         mode,
			FirstMessage: content,
		})
		if err != nil {
			if errors.Is(err, chat.ErrTooManySessions) {
				return searchTurn{}, true, writeError(e, http.StatusConflict, tooManySessionsMessage)
			}
			app.Logger().Error("search session create failed", slog.Any("error", err))
			return searchTurn{}, true, writeError(e, http.StatusInternalServerError, "Search is unavailable.")
		}
		opened = session
	}

	return searchTurn{
		agent:          agent,
		session:        session,
		opened:         opened,
		ownerID:        ownerID,
		runID:          strings.TrimSpace(req.RunID),
		content:        content,
		mode:           mode,
		messages:       append(history, ai.ChatMessage{Role: chat.RoleUser, Content: content}),
		tools:          tools,
		priorDocuments: priorDocuments,
	}, false, nil
}

// persistSearchTurn stores the exchange and renders what the client gets back.
//
// A storage failure is not allowed to swallow the answer: the provider has
// already been paid for it, so the reply is handed over unsaved and the
// conversation simply does not become resumable -- which is why a session this
// request opened is dropped again on that path. The session cap cannot surface
// here at all any more: prepareSearchTurn answers it before the run starts.
func persistSearchTurn(app core.App, t searchTurn, reply string, hits []ai.DocumentHit) searchResponse {
	session, err := chat.AppendTurn(app, t.ownerID, t.session.Id, chat.Turn{
		UserContent:      t.content,
		AssistantContent: reply,
		Documents:        hits,
		Mode:             t.mode,
	})
	if err != nil {
		app.Logger().Error("search persist failed", slog.Any("error", err))
		discardEmptySession(app, t.opened)
		return searchResponse{
			Message:   unsavedMessage(chat.RoleAssistant, reply, hits),
			Documents: hits,
			Saved:     false,
			Detail:    "This answer could not be saved, so the chat will not appear in your history.",
		}
	}

	info := chat.ToSessionInfo(session)
	return searchResponse{
		Session:   &info,
		Message:   latestAssistantMessage(app, session.Id, reply, hits),
		Documents: hits,
		Saved:     true,
	}
}

func handleDeepSearch(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		turn, handled, err := prepareSearchTurn(app, rt, idx, e)
		if handled {
			return err
		}

		// Detached from the connection. This endpoint writes nothing until the
		// whole answer is ready, so it is silent for its entire duration --
		// exactly what a reverse proxy with a read timeout hangs up on. Tying
		// the agent loop to the socket meant such a hangup cancelled the run
		// and discarded an answer the provider had already been paid for. Now
		// the run finishes and the turn is stored either way; only the delivery
		// of this response depends on the caller still being there.
		ctx, stopRun := startSearchRun(e.Request.Context(), turn.ownerID, turn.runID)
		defer stopRun()

		var reply string
		var hits []ai.DocumentHit
		incomplete := false
		if turn.research() {
			// Non-streaming fallback for clients that cannot read SSE.
			result, researchErr := turn.agent.Research(turn.agentContext(ctx), ai.ResearchRequest{
				Messages:       turn.messages,
				AvailableTags:  turn.tools.tags,
				Search:         turn.tools.search,
				Read:           turn.tools.read,
				PriorDocuments: turn.priorDocuments,
				DenseRetrieval: turn.tools.dense,
				Survey:         turn.tools.survey,
				Count:          turn.tools.count,
			}, nil)
			reply, hits, incomplete, err = result.Reply, result.Documents, result.Incomplete, researchErr
		} else {
			reply, hits, err = turn.agent.Search(turn.agentContext(ctx), turn.messages, turn.tools.tags, turn.tools.search, ai.SearchOptions{DenseRetrieval: turn.tools.dense})
		}
		if err != nil {
			discardEmptySession(app, turn.opened)
			// Running out of budget is not the provider failing, and saying so
			// sends the caller to check an AI configuration that is fine.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				app.Logger().Warn("deep search ran out of budget", "mode", turn.mode, "budget", searchRunBudget.String())
				return writeError(e, http.StatusGatewayTimeout, runTooLongMessage)
			}
			app.Logger().Error("deep search failed", "mode", turn.mode, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "The AI provider could not complete the search.")
		}
		if hits == nil {
			hits = []ai.DocumentHit{}
		}

		response := persistSearchTurn(app, turn, reply, hits)
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

// handleSearchStream runs a search turn over SSE. Research reports each step as
// it happens, then streams the answer -- a research run can spend a minute
// searching and reading, which is far too long to show as a single spinner.
// Plain search has no steps to report, but it streams anyway, for the
// heartbeat: a response that writes nothing until it is finished looks
// indistinguishable from a hung backend to whatever proxy sits in front.
//
// This used to refuse anything but research, to stop a client that omitted the
// mode from being billed for the expensive one. Serving both makes that guard
// unnecessary rather than absent: an unrecognised mode parses as "search", so
// the failure of omitting it is now a cheap search rather than a costly
// surprise.
func handleSearchStream(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		turn, handled, err := prepareSearchTurn(app, rt, idx, e)
		if handled {
			return err
		}

		// Everything below is streamed, so errors are reported as events —
		// the status line has already been written by this point.
		stream := newSSEWriter(e)
		// Every model completion is a silent gap on this connection, and the
		// first one comes before any step event. Stopped before returning.
		//
		// On the request context, not the run's: this keeps the socket warm
		// while someone is listening, and there is nothing to keep warm once
		// nobody is.
		stopHeartbeat := stream.Heartbeat(e.Request.Context())
		defer stopHeartbeat()

		// Detached from the connection, so a dropped socket costs the view of
		// the run and not the run itself. The answer is stored below whether or
		// not anyone is still reading this stream, which is the difference
		// between a network blip losing a paragraph of progress and losing a
		// finished, already-paid-for answer. Deliberate cancellation comes
		// through /search/cancel instead -- see startSearchRun.
		ctx, stopRun := startSearchRun(e.Request.Context(), turn.ownerID, turn.runID)
		defer stopRun()
		ctx = turn.agentContext(ctx)

		var result ai.ResearchResult
		if turn.research() {
			result, err = turn.agent.Research(ctx, ai.ResearchRequest{
				Messages:       turn.messages,
				AvailableTags:  turn.tools.tags,
				Search:         turn.tools.search,
				Read:           turn.tools.read,
				PriorDocuments: turn.priorDocuments,
				DenseRetrieval: turn.tools.dense,
				Survey:         turn.tools.survey,
				Count:          turn.tools.count,
			}, func(event ai.ResearchEvent) { stream.Send(event) })
		} else {
			reply, hits, searchErr := turn.agent.Search(ctx, turn.messages, turn.tools.tags, turn.tools.search, ai.SearchOptions{DenseRetrieval: turn.tools.dense})
			result, err = ai.ResearchResult{Reply: reply, Documents: hits}, searchErr
		}
		if err != nil {
			// Either way the conversation this request opened never got a turn.
			discardEmptySession(app, turn.opened)
			if runErr := ctx.Err(); runErr != nil {
				// The run itself was stopped -- out of budget, or cancelled
				// through /search/cancel. Not the same as the client merely
				// hanging up, which no longer reaches here at all.
				app.Logger().Info("search run stopped", "mode", turn.mode, slog.Any("error", runErr))
				// Someone may still be watching. A cancel they asked for needs
				// no explanation, but a run that ran out of budget would
				// otherwise end as a bare EOF, which the page can only report
				// as having produced no answer at all.
				if errors.Is(runErr, context.DeadlineExceeded) {
					stream.Send(ai.ResearchEvent{Type: "error", Message: runTooLongMessage})
				}
				stream.Send(ai.ResearchEvent{Type: "done"})
				return nil
			}
			app.Logger().Error("search run failed", "mode", turn.mode, slog.Any("error", err))
			stream.Send(ai.ResearchEvent{Type: "error", Message: "The AI provider could not complete the search."})
			stream.Send(ai.ResearchEvent{Type: "done"})
			return nil
		}

		documents := result.Documents
		if documents == nil {
			documents = []ai.DocumentHit{}
		}

		// Stored before anything else is written, and this order is the point.
		// The session has been there since before the run started; the turn is
		// what was missing, and it must not be made to wait behind a socket.
		// A write to a half-closed connection can block until the kernel gives
		// up on it, and every one of those blocked between a finished answer
		// and the save that keeps it -- which is the failure this whole change
		// exists to end. Unconditional for the same reason: a client that hung
		// up mid-run is exactly the case that must still find its answer
		// waiting in the sidebar.
		saved := persistSearchTurn(app, turn, result.Reply, documents)

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
		// For a client still here, this is what makes the conversation
		// resumable, not what makes the answer visible -- that arrived above.
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

type searchCancelRequest struct {
	RunID string `json:"run_id"`
}

// handleSearchCancel stops a run the caller started. It exists because runs no
// longer die with their connection: to the server a closed socket is a closed
// socket, whether the user pressed Cancel or their wifi dropped, and only one
// of those should throw the work away. So stopping is said out loud, here.
//
// An id that is not running answers 200 all the same -- a run that finished
// while the cancel was in flight is not a client error, and saying so would
// only give the page an error to render over a result that is already correct.
func handleSearchCancel(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req searchCancelRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		// Scoped to the owner inside cancelSearchRun, so a guessed id from
		// another account finds nothing.
		stopped := cancelSearchRun(ownerID, strings.TrimSpace(req.RunID))
		return writeJSON(e, http.StatusOK, map[string]bool{"stopped": stopped})
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
