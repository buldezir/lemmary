package appapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/fulltext"
)

// maxAvailableTagNames caps how many tag names are inlined into the agent prompt.
const maxAvailableTagNames = 500

// modeResearch is the only mode worth naming: anything else — including a
// legacy "shallow" or "deep" from an older client — is plain search.
const modeResearch = "research"

type searchRequest struct {
	Messages []ai.ChatMessage `json:"messages"`
	Mode     string           `json:"mode"`
}

type searchResponse struct {
	Message   ai.ChatMessage   `json:"message"`
	Documents []ai.DocumentHit `json:"documents"`
	// Set when a research answer was cut off mid-generation; see
	// ai.ResearchResult.Incomplete.
	Incomplete bool `json:"incomplete,omitempty"`
}

func isResearchMode(mode string) bool {
	return strings.EqualFold(strings.TrimSpace(mode), modeResearch)
}

// agentTools resolves the per-request scoping shared by both modes: the tag
// catalogue offered to the agent, and the searcher/reader closures bound to the
// caller's own documents.
type agentTools struct {
	tags   []string
	search ai.DocumentSearcher
	read   ai.DocumentReader
}

func buildAgentTools(app core.App, idx *fulltext.Index, e *core.RequestEvent) (agentTools, error) {
	// Match homepage document listing: regular users are scoped to their own
	// docs; superusers bypass ownership (PocketBase collection rules do the same).
	userID := ""
	if !e.HasSuperuserAuth() {
		userID = e.Auth.Id
	}
	tags, err := listAvailableTagNames(app, userID)
	if err != nil {
		return agentTools{}, err
	}
	return agentTools{
		tags: tags,
		search: func(ctx context.Context, args ai.SearchDocumentsArgs) ([]ai.DocumentHit, error) {
			return searchUserDocuments(app, idx, userID, args)
		},
		read: func(ctx context.Context, ids []string, maxTotalChars int) ([]ai.DocumentContent, error) {
			return readUserDocuments(app, userID, ids, maxTotalChars)
		},
	}, nil
}

func handleDeepSearch(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req searchRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if len(req.Messages) == 0 {
			return writeError(e, http.StatusBadRequest, "At least one message is required.")
		}

		agent := rt.Snapshot().SearchAgent
		if agent == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI search is not configured; update Settings.")
		}

		tools, err := buildAgentTools(app, idx, e)
		if err != nil {
			app.Logger().Error("deep search list tags failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Search is unavailable.")
		}

		// Use the request context so closing the browser tab cancels the agent
		// loop instead of leaving several LLM round-trips running.
		var reply string
		var hits []ai.DocumentHit
		incomplete := false
		if isResearchMode(req.Mode) {
			// Non-streaming fallback for clients that cannot read SSE.
			result, researchErr := agent.Research(e.Request.Context(), ai.ResearchRequest{
				Messages:      req.Messages,
				AvailableTags: tools.tags,
				Search:        tools.search,
				Read:          tools.read,
			}, nil)
			reply, hits, incomplete, err = result.Reply, result.Documents, result.Incomplete, researchErr
		} else {
			reply, hits, err = agent.Search(e.Request.Context(), req.Messages, tools.tags, tools.search)
		}
		if err != nil {
			app.Logger().Error("deep search failed", "mode", req.Mode, slog.Any("error", err))
			return writeError(e, http.StatusBadGateway, "The AI provider could not complete the search.")
		}
		if hits == nil {
			hits = []ai.DocumentHit{}
		}

		return writeJSON(e, http.StatusOK, searchResponse{
			Message: ai.ChatMessage{
				Role:    "assistant",
				Content: reply,
			},
			Documents:  hits,
			Incomplete: incomplete,
		})
	}
}

// handleResearchStream runs a research turn and reports each step as it
// happens, then streams the answer. A research run can spend a minute searching
// and reading, which is far too long to show as a single spinner.
func handleResearchStream(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req searchRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if len(req.Messages) == 0 {
			return writeError(e, http.StatusBadRequest, "At least one message is required.")
		}
		// This endpoint only ever researches, and research is the expensive
		// mode. Without this, a client that omitted the field — or sent a
		// legacy "deep" — would get a full research run out of what it thought
		// was a plain search.
		if !isResearchMode(req.Mode) {
			return writeError(e, http.StatusBadRequest, `This endpoint streams research; send mode "research" or use /api/app/search.`)
		}

		agent := rt.Snapshot().SearchAgent
		if agent == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI search is not configured; update Settings.")
		}

		tools, err := buildAgentTools(app, idx, e)
		if err != nil {
			app.Logger().Error("research list tags failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Search is unavailable.")
		}

		// Everything below is streamed, so errors are reported as events —
		// the status line has already been written by this point.
		stream := newSSEWriter(e)
		// Every model completion is a silent gap on this connection, and the
		// first one comes before any step event. Stopped before returning.
		stopHeartbeat := stream.Heartbeat(e.Request.Context())
		defer stopHeartbeat()

		result, err := agent.Research(e.Request.Context(), ai.ResearchRequest{
			Messages:      req.Messages,
			AvailableTags: tools.tags,
			Search:        tools.search,
			Read:          tools.read,
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
