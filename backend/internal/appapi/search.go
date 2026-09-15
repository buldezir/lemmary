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
	"lemmary/backend/internal/websearch"
)

const maxAvailableTagNames = 500

type searchRequest struct {
	// SessionID continues an existing conversation; empty starts a new one.
	SessionID string `json:"session_id"`
	Content   string `json:"content"`
	Mode      string `json:"mode"`
	// RunID lets the client cancel this run explicitly: a run outlives its
	// connection, so hanging up does not stop it. Optional; omitting it costs
	// the ability to cancel, nothing else.
	RunID string `json:"run_id"`
	// The provider and model to open the conversation on, instead of the search
	// binding in Settings. Read only when SessionID is empty, as Mode is.
	ProviderID string `json:"provider_id"`
	Model      string `json:"model"`
	// ForkFrom continues another conversation in a copy of it, leaving the
	// original untouched. Read only when SessionID is empty, as Mode and the
	// binding are; the copy takes its kind, mode and binding from the source.
	ForkFrom string `json:"fork_from"`
	// Web lets this turn reach the public web. Per turn rather than stored with
	// the conversation: unlike Mode, nothing in the transcript depends on it,
	// and a metered tool is better defaulted off on every reload.
	Web bool `json:"web"`
}

type searchResponse struct {
	// Session is null when Saved is false -- see the AppendTurn failure path.
	Session   *chat.SessionInfo `json:"session"`
	Message   chat.MessageInfo  `json:"message"`
	Documents []ai.DocumentHit  `json:"documents"`
	Saved     bool              `json:"saved"`
	// Set when a research answer was cut off mid-generation; stored on the
	// assistant row so a reopened chat can still say so.
	Incomplete bool `json:"incomplete,omitempty"`
	// Why the turn could not be saved, when Saved is false.
	Detail string `json:"detail,omitempty"`
}

type searchTurn struct {
	agent ai.SearchAgent
	// opened holds the same record as session only when this request created
	// it, which is what may be taken back when the turn never lands.
	session  *core.Record
	opened   *core.Record
	ownerID  string
	runID    string
	content  string
	mode     string
	messages []ai.ChatMessage
	tools    agentTools
	// priorDocuments are earlier turns' hits, readable by id without searching
	// for them again.
	priorDocuments []ai.DocumentHit
}

func (t searchTurn) research() bool { return t.mode == chat.ModeResearch }

// agentContext names the conversation on the context, so every completion the
// loop makes reaches the provider under one cache key.
func (t searchTurn) agentContext(parent context.Context) context.Context {
	return aiprovider.WithSession(parent, t.session.Id)
}

type agentTools struct {
	tags   []string
	search ai.DocumentSearcher
	read   ai.DocumentReader
	// survey and count are nil when unavailable: no helper model for a
	// survey, no database for a count.
	survey ai.DocumentSurveyor
	count  ai.DocumentCounter
	// dense is set when the retriever has an embedding leg; the prompt is
	// worded differently for a search that crosses languages by itself.
	dense bool
	// web backs web_search and web_fetch. Nil unless a provider is bound and
	// the request asked for it, and the tools are then not offered.
	web *websearch.Tavily
}

// buildAgentTools binds one retriever per request, shared by both closures so
// per-turn work is done once. The dense half is attached only when both an
// embedder and a chunk index exist; either one missing leaves keywords alone.
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

// prepareSearchTurn does the work both search handlers share. On failure it
// writes the response itself and reports handled; it runs before anything is
// streamed, so a failure here is still an ordinary HTTP error.
func prepareSearchTurn(app core.App, rt *config.Runtime, idx *fulltext.Index, e *core.RequestEvent) (searchTurn, bool, error) {
	var req searchRequest
	if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
		return searchTurn{}, true, writeError(e, http.StatusBadRequest, "Invalid request body.")
	}
	content, err := validateChatContent(req.Content)
	if err != nil {
		return searchTurn{}, true, writeError(e, http.StatusBadRequest, err.Error())
	}
	runID, err := validateRunID(req.RunID)
	if err != nil {
		return searchTurn{}, true, writeError(e, http.StatusBadRequest, err.Error())
	}

	// ownerID is whose sidebar this conversation belongs in, so a superuser
	// resolves to its paired users record; searchUserID is whose documents the
	// search may see, where a superuser stays unscoped. Collapsing them would
	// either hide an admin's own chats or scope an admin's search to one account.
	ownerID, err := resolveOwnerUserID(app, e)
	if err != nil {
		return searchTurn{}, true, writeOwnerError(e, err)
	}
	searchUserID := ""
	if !e.HasSuperuserAuth() {
		searchUserID = e.Auth.Id
	}

	// A fork reads the conversation it branches from exactly as a continuation
	// would: same history, same mode, same binding. Only the session the turn
	// lands in differs, and that is decided further down.
	sourceID := strings.TrimSpace(req.SessionID)
	forking := false
	if sourceID == "" {
		if forkFrom := strings.TrimSpace(req.ForkFrom); forkFrom != "" {
			sourceID = forkFrom
			forking = true
		}
	}

	session, history, err := loadChatHistory(app, ownerID, sourceID, chat.KindSearch, "")
	if err != nil {
		return searchTurn{}, true, writeChatSessionError(e, app, err)
	}

	// A conversation stays in the mode it started in, and this is where that
	// holds rather than in the page that hides the switch: the replayed
	// transcript was produced by one mode, and the other would read that work
	// back as its own. Refused rather than corrected, since a client sending the
	// wrong mode means the two have drifted.
	mode := parseSearchMode(req.Mode)
	if session != nil {
		if stored := session.GetString("mode"); stored != "" && stored != mode {
			return searchTurn{}, true, writeError(e, http.StatusConflict,
				"This chat is a "+stored+" chat and cannot change mode. Start a new chat to switch.")
		}
	}

	// After the session, so a continued conversation runs on the binding stored
	// with it rather than on whatever the request echoed back. The search
	// binding, not the chat one: an instance that bound them separately meant it.
	cfg := rt.Snapshot().Cfg
	requested := aiprovider.Binding{ProviderID: req.ProviderID, Model: req.Model}
	binding := conversationBinding(session, recordedBinding(requested, cfg.SearchProviderID, cfg.SearchModel))
	snap, err := conversationSnapshot(app, rt, config.Overrides{Search: binding}, session, requested)
	if err != nil {
		return searchTurn{}, true, writeError(e, http.StatusBadRequest, err.Error())
	}
	agent := snap.SearchAgent
	if agent == nil {
		return searchTurn{}, true, writeError(e, http.StatusServiceUnavailable, "AI search is not configured; update Settings.")
	}

	tools, err := buildAgentTools(app, rt, idx, searchUserID)
	if err != nil {
		app.Logger().Error("search list tags failed", slog.Any("error", err))
		return searchTurn{}, true, writeError(e, http.StatusInternalServerError, "Search is unavailable.")
	}
	// Research only: search mode is one round against the archive, and paying a
	// metered provider for a turn that renders a card list serves nobody.
	if req.Web && mode == chat.ModeResearch {
		tools.web = snap.WebSearch
	}

	// A follow-up is usually about what the last answer cited, so carrying the
	// hits saves the model guessing a query to rediscover them.
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
	if session == nil || forking {
		var created *core.Record
		var createErr error
		if forking {
			// From here `session` is the copy, so the run, the session frame and
			// the stored turn all name the fork; the source keeps the transcript
			// it had when this request read it. Deliberately not taken back when
			// the run fails, unlike an empty conversation: the copy is named to
			// the client before the first provider call, and by then the page is
			// standing in it.
			created, createErr = chat.ForkSession(app, ownerID, session)
		} else {
			created, createErr = chat.CreateSession(app, chat.NewSession{
				UserID:       ownerID,
				Kind:         chat.KindSearch,
				Mode:         mode,
				Binding:      binding,
				FirstMessage: content,
			})
		}
		if createErr != nil {
			if errors.Is(createErr, chat.ErrTooManySessions) {
				return searchTurn{}, true, writeError(e, http.StatusConflict, tooManySessionsMessage)
			}
			app.Logger().Error("search session create failed", "forked", forking, slog.Any("error", createErr))
			return searchTurn{}, true, writeError(e, http.StatusInternalServerError, "Search is unavailable.")
		}
		session, opened = created, created
	}

	return searchTurn{
		agent:          agent,
		session:        session,
		opened:         opened,
		ownerID:        ownerID,
		runID:          runID,
		content:        content,
		mode:           mode,
		messages:       append(history, ai.ChatMessage{Role: chat.RoleUser, Content: content}),
		tools:          tools,
		priorDocuments: priorDocuments,
	}, false, nil
}

// persistSearchTurn must not let a storage failure swallow the answer: the
// provider has already been paid for it, so the reply is handed over unsaved
// and a session this request opened is dropped again.
func persistSearchTurn(app core.App, t searchTurn, reply string, hits []ai.DocumentHit, steps []chat.StoredStep, incomplete bool) searchResponse {
	session, err := chat.AppendTurn(app, t.ownerID, t.session.Id, chat.Turn{
		UserContent:      t.content,
		AssistantContent: reply,
		RunID:            t.runID,
		Documents:        hits,
		Steps:            steps,
		Incomplete:       incomplete,
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
		Message:   latestAssistantMessage(app, session.Id, t.runID, reply, hits),
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

		// Detached from the connection: this endpoint is silent until the whole
		// answer is ready, which is what a proxy read timeout hangs up on. The
		// run finishes and the turn is stored either way; only the delivery of
		// this response depends on the caller still being there.
		ctx, stopRun := startDetachedRun(e.Request.Context(), turn.ownerID, turn.runID, turn.session.Id)
		defer stopRun()

		var reply string
		var hits []ai.DocumentHit
		incomplete := false
		var steps []chat.StoredStep
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
				Web:            turn.tools.web,
			}, func(event ai.ResearchEvent) {
				if event.Type == "step" {
					steps = append(steps, chat.StepFromEvent(event))
				}
			})
			reply, hits, incomplete, err = result.Reply, result.Documents, result.Incomplete, researchErr
		} else {
			reply, hits, err = turn.agent.Search(turn.agentContext(ctx), turn.messages, turn.tools.tags, turn.tools.search, ai.SearchOptions{DenseRetrieval: turn.tools.dense})
		}
		if err != nil {
			discardEmptySession(app, turn.opened)
			// Running out of budget is not the provider failing, and saying so
			// sends the caller to check an AI configuration that is fine.
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				app.Logger().Warn("deep search ran out of budget", "mode", turn.mode, "budget", detachedRunBudget.String())
				return writeError(e, http.StatusGatewayTimeout, runTooLongMessage)
			}
			app.Logger().Error("deep search failed", "mode", turn.mode, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "The AI provider could not complete the search.")
		}
		if hits == nil {
			hits = []ai.DocumentHit{}
		}

		response := persistSearchTurn(app, turn, reply, hits, steps, incomplete)
		response.Incomplete = incomplete
		return writeJSON(e, http.StatusOK, response)
	}
}

// searchStartedEvent is sent so a client that loses the connection knows which
// conversation to find the stored turn in.
type searchStartedEvent struct {
	Type    string           `json:"type"`
	Session chat.SessionInfo `json:"session"`
}

// searchSavedEvent closes the stream with the stored turn. Saved is false when
// the answer was produced but could not be stored, and Detail then says why.
type searchSavedEvent struct {
	Type      string            `json:"type"`
	Session   *chat.SessionInfo `json:"session"`
	Message   chat.MessageInfo  `json:"message"`
	Documents []ai.DocumentHit  `json:"documents"`
	Saved     bool              `json:"saved"`
	Detail    string            `json:"detail,omitempty"`
}

// handleSearchStream runs a search turn over SSE. Plain search has no steps to
// report but streams anyway, for the heartbeat: a response that writes nothing
// until it is finished is indistinguishable from a hung backend to a proxy.
// An unrecognised mode parses as "search", so omitting it costs a cheap search
// rather than a billed research run.
func handleSearchStream(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		turn, handled, err := prepareSearchTurn(app, rt, idx, e)
		if handled {
			return err
		}

		// Register before sending the session frame: recovery treats that frame
		// as proof the run started, and sending it first leaves a window where an
		// immediate poll sees running=false.
		ctx, stopRun := startDetachedRun(e.Request.Context(), turn.ownerID, turn.runID, turn.session.Id)
		defer stopRun()
		ctx = turn.agentContext(ctx)

		// Everything below is streamed, so errors are reported as events: the
		// status line has already been written by this point.
		stream := newSSEWriter(e)
		// First frame, before a single provider call: a client whose connection
		// dies mid-run has nowhere to collect the answer from if it never learnt
		// the session id, which is the case on a new chat's first question.
		started := chat.ToSessionInfo(turn.session)
		stream.Send(searchStartedEvent{Type: "session", Session: started})
		// Every model completion is a silent gap on this connection, and the
		// first one comes before any step event. On the request context, not the
		// run's: there is nothing to keep warm once nobody is listening.
		stopHeartbeat := stream.Heartbeat(e.Request.Context())
		defer stopHeartbeat()

		// Detached from the connection, so a dropped socket costs the view of the
		// run and not the already-paid-for answer, which is stored below either
		// way. Deliberate cancellation comes through /search/cancel instead.
		var steps []chat.StoredStep
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
				Web:            turn.tools.web,
			}, func(event ai.ResearchEvent) {
				if event.Type == "step" {
					steps = append(steps, chat.StepFromEvent(event))
				}
				stream.Send(event)
			})
		} else {
			reply, hits, searchErr := turn.agent.Search(ctx, turn.messages, turn.tools.tags, turn.tools.search, ai.SearchOptions{DenseRetrieval: turn.tools.dense})
			result, err = ai.ResearchResult{Reply: reply, Documents: hits}, searchErr
		}
		if err != nil {
			// Either way the conversation this request opened never got a turn.
			discardEmptySession(app, turn.opened)
			if runErr := ctx.Err(); runErr != nil {
				// The run itself was stopped, out of budget or cancelled. Not the
				// client merely hanging up, which does not reach here.
				app.Logger().Info("search run stopped", "mode", turn.mode, slog.Any("error", runErr))
				// A cancel the viewer asked for needs no explanation, but a
				// run out of budget would otherwise end as a bare EOF.
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

		// Stored before anything is written, and unconditionally: a write to a
		// half-closed connection can block until the kernel gives up, and that
		// must never sit between a finished answer and the save that keeps it.
		saved := persistSearchTurn(app, turn, result.Reply, documents, steps, result.Incomplete)

		stream.Send(ai.ResearchEvent{Type: "documents", Documents: documents})
		// The whole answer follows the deltas: those are a live preview, this is
		// the citation-checked text. Incomplete says whether a generation that
		// outran the timeout was kept short, so the client can say so.
		stream.Send(ai.ResearchEvent{
			Type:       "message",
			Content:    result.Reply,
			Incomplete: result.Incomplete,
		})
		// This makes the conversation resumable, not the answer visible; that
		// arrived above.
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
	// SessionID stops every run on a conversation instead, for a page that
	// reloaded mid-run. Ignored when RunID is set.
	SessionID string `json:"session_id"`
}

// handleSearchCancel exists because runs do not die with their connection: a
// closed socket looks the same whether the user pressed Cancel or their wifi
// dropped, so stopping is said out loud. An id that is not running answers 200
// all the same, since a run that finished mid-cancel is not a client error.
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
		if runID := strings.TrimSpace(req.RunID); runID != "" {
			return writeJSON(e, http.StatusOK, map[string]bool{"stopped": cancelSearchRun(ownerID, runID)})
		}
		// The registry has no owners, so the ownership check happens here, on
		// the session record, before anything is stopped.
		sessionID := strings.TrimSpace(req.SessionID)
		if _, err := chat.FindOwnedSession(app, ownerID, sessionID); err != nil {
			if errors.Is(err, chat.ErrNotFound) {
				return writeJSON(e, http.StatusOK, map[string]bool{"stopped": false})
			}
			return writeError(e, http.StatusInternalServerError, "Failed to cancel the run.")
		}
		return writeJSON(e, http.StatusOK, map[string]bool{"stopped": cancelSessionRuns(sessionID)})
	}
}

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
