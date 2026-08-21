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
	"paperless-go/backend/internal/ai"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/fulltext"
)

// maxAvailableTagNames caps how many tag names are inlined into the agent prompt.
const maxAvailableTagNames = 500

type searchRequest struct {
	Messages []ai.ChatMessage `json:"messages"`
	Mode     string           `json:"mode"`
}

type searchResponse struct {
	Message   ai.ChatMessage   `json:"message"`
	Documents []ai.DocumentHit `json:"documents"`
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

		mode := ai.SearchModeShallow
		if strings.EqualFold(strings.TrimSpace(req.Mode), string(ai.SearchModeDeep)) {
			mode = ai.SearchModeDeep
		}

		agent := rt.Snapshot().SearchAgent
		if agent == nil {
			return writeError(e, http.StatusServiceUnavailable, "AI search is not configured; update Settings.")
		}

		// Match homepage document listing: regular users are scoped to their own
		// docs; superusers bypass ownership (PocketBase collection rules do the same).
		userID := ""
		if !e.HasSuperuserAuth() {
			userID = e.Auth.Id
		}
		availableTags, err := listAvailableTagNames(app, userID)
		if err != nil {
			app.Logger().Error("deep search list tags failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Search is unavailable.")
		}

		searcher := func(ctx context.Context, args ai.SearchDocumentsArgs) ([]ai.DocumentHit, error) {
			return searchUserDocuments(app, idx, userID, args)
		}

		// Use the request context so closing the browser tab cancels the agent
		// loop instead of leaving several LLM round-trips running.
		reply, hits, err := agent.Search(e.Request.Context(), req.Messages, mode, availableTags, searcher)
		if err != nil {
			app.Logger().Error("deep search failed", slog.Any("error", err))
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
			Documents: hits,
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
