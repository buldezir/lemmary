package appapi

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/retrieval"
)

const (
	defaultListPageSize   = 12
	maxListPageSize       = 100
	maxDenseListDocuments = 20
	// Of (score - median) / (1 - median), measured on bge-m3: unrelated
	// queries top out at 0.12, cross-language product matches start at 0.155.
	minMeaningGap = 0.15
	// ponytail: past this many keyword matches the list falls back to keyword
	// ranking alone; page the fusion if archives outgrow it.
	maxFusedKeywordMatches = 5000
	// A down provider otherwise holds every keystroke through its retries.
	maxDenseWait = 2 * time.Second
)

// Similarities are only comparable within one model, so only a measured model
// has a floor. On bge-m3 unrelated queries top out just under 0.48 and
// "invoice" over German invoices starts there.
func similarityFloor(model string) float64 {
	if strings.Contains(strings.ToLower(model), "bge-m3") {
		return 0.48
	}
	return 0
}

type documentSearchList struct {
	Page       int              `json:"page"`
	PerPage    int              `json:"perPage"`
	TotalItems int              `json:"totalItems"`
	TotalPages int              `json:"totalPages"`
	Items      []map[string]any `json:"items"`
}

func handleDocumentSearch(app core.App, rt *config.Runtime, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		q := strings.TrimSpace(e.Request.URL.Query().Get("q"))
		if q == "" {
			return writeError(e, http.StatusBadRequest, "q is required.")
		}
		if idx == nil || !idx.Ready() {
			return writeError(e, http.StatusServiceUnavailable, "Search index is not ready.")
		}

		page := queryPositiveInt(e, "page", 1)
		perPage := queryPositiveInt(e, "perPage", defaultListPageSize)
		if perPage > maxListPageSize {
			perPage = maxListPageSize
		}

		userID := ""
		if !e.HasSuperuserAuth() {
			userID = e.Auth.Id
		}

		status := strings.TrimSpace(e.Request.URL.Query().Get("status"))
		typeID := strings.TrimSpace(e.Request.URL.Query().Get("document_type"))
		corrID := strings.TrimSpace(e.Request.URL.Query().Get("correspondent"))

		query := fulltext.Query{
			Text:             q,
			UserID:           userID,
			ProcessingStatus: status,
			DateFrom:         strings.TrimSpace(e.Request.URL.Query().Get("date_from")),
			DateTo:           strings.TrimSpace(e.Request.URL.Query().Get("date_to")),
			Undated:          e.Request.URL.Query().Get("undated") == "true",
			Untagged:         e.Request.URL.Query().Get("untagged") == "true",
			Owner:            e.Request.URL.Query().Get("owner"),
			Offset:           (page - 1) * perPage,
			Limit:            perPage,
		}
		if typeID != "" && typeID != "all" {
			query.DocumentTypeIDs = []string{typeID}
		}
		if corrID != "" && corrID != "all" {
			query.CorrespondentIDs = []string{corrID}
		}
		for _, tagID := range strings.Split(e.Request.URL.Query().Get("tags"), ",") {
			if tagID = strings.TrimSpace(tagID); tagID != "" && tagID != "all" {
				query.AllTagIDs = append(query.AllTagIDs, tagID)
			}
		}

		var (
			result fulltext.Result
			fused  bool
			err    error
		)
		if embedder := rt.Snapshot().Embedder; embedder != nil && idx.ChunksReady() {
			r := &agentRetriever{app: app, idx: idx, userID: userID, embedQuery: embedQueryFunc(embedder), chunks: idx}
			result, fused, err = r.fusedDocumentPage(e.Request.Context(), query, similarityFloor(embedder.Model()))
		}
		if err == nil && !fused {
			result, err = idx.Search(query)
		}
		if err != nil {
			app.Logger().Error("document search failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Search failed.")
		}

		items := hydrateDocumentExports(app, result.Hits, userID)
		totalPages := 0
		if perPage > 0 && result.Total > 0 {
			totalPages = int((result.Total + uint64(perPage) - 1) / uint64(perPage))
		}

		return writeJSON(e, http.StatusOK, documentSearchList{
			Page:       page,
			PerPage:    perPage,
			TotalItems: int(result.Total),
			TotalPages: totalPages,
			Items:      items,
		})
	}
}

// fusedDocumentPage ranks every keyword match together with the documents the
// chunk index finds by meaning, the way the agent's search does, then cuts the
// page. Meaning reorders the keyword matches but never outranks them: the box
// is a filter, and a hit without the words above one with them reads as a bug.
// ok is false when there are too many keyword matches to rank here.
func (r *agentRetriever) fusedDocumentPage(ctx context.Context, q fulltext.Query, floor float64) (fulltext.Result, bool, error) {
	ids, _, complete, err := r.idx.MatchingIDs(q, maxFusedKeywordMatches)
	if err != nil || !complete {
		return fulltext.Result{}, false, err
	}
	denseCtx, cancel := context.WithTimeout(ctx, maxDenseWait)
	defer cancel()
	var dense []retrieval.Ranked
	if chunkHits := r.searchChunks(denseCtx, q, q.Text, "", maxDenseListDocuments*denseCandidateFactor); len(chunkHits) > 0 {
		dense, _ = retrieval.GroupChunks(chunkHits, 1)
		dense = retrieval.Standouts(dense, floor, minMeaningGap)
		dense = dense[:min(len(dense), maxDenseListDocuments)]
	}
	ranked := retrieval.RRF(retrieval.Rank(ids), dense)
	keyword := make(map[string]bool, len(ids))
	for _, id := range ids {
		keyword[id] = true
	}
	sort.SliceStable(ranked, func(i, j int) bool { return keyword[ranked[i].ID] && !keyword[ranked[j].ID] })
	fused := retrieval.IDs(ranked)

	start := min(max(q.Offset, 0), len(fused))
	end := min(start+q.Limit, len(fused))
	hits := make([]fulltext.Hit, 0, end-start)
	for _, id := range fused[start:end] {
		hits = append(hits, fulltext.Hit{ID: id})
	}
	return fulltext.Result{Hits: hits, Total: uint64(len(fused))}, true, nil
}

func handleSearchReindex(app core.App, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if idx == nil {
			return writeError(e, http.StatusServiceUnavailable, "Search index is not ready.")
		}
		n, err := idx.Rebuild(app)
		if err != nil {
			app.Logger().Error("search reindex failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Reindex failed.")
		}
		return writeJSON(e, http.StatusOK, map[string]any{"indexed": n})
	}
}

type documentLookup interface {
	FindRecordById(collectionNameOrId any, recordId string, optFilters ...func(*dbx.SelectQuery) error) (*core.Record, error)
	FindFirstRecordByFilter(collectionModelOrIdentifier any, filter string, params ...dbx.Params) (*core.Record, error)
	ExpandRecord(record *core.Record, expands []string, optFetchFunc core.ExpandFetchFunc) map[string]error
}

func hydrateDocumentExports(app documentLookup, hits []fulltext.Hit, userID string) []map[string]any {
	items := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		rec, err := app.FindRecordById("documents", hit.ID)
		if err != nil {
			continue
		}
		if !CanReadDocument(app, rec, userID) {
			continue
		}
		_ = app.ExpandRecord(rec, []string{"tags", "document_type", "correspondent", "duplicate_of"}, nil)
		items = append(items, rec.PublicExport())
	}
	if items == nil {
		items = []map[string]any{}
	}
	return items
}

func queryPositiveInt(e *core.RequestEvent, name string, fallback int) int {
	raw := strings.TrimSpace(e.Request.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
