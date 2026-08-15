package appapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/fulltext"
)

const (
	defaultListPageSize = 12
	maxListPageSize     = 100
)

type documentSearchList struct {
	Page       int              `json:"page"`
	PerPage    int              `json:"perPage"`
	TotalItems int              `json:"totalItems"`
	TotalPages int              `json:"totalPages"`
	Items      []map[string]any `json:"items"`
}

func handleDocumentSearch(app core.App, idx *fulltext.Index) func(*core.RequestEvent) error {
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
			Offset:           (page - 1) * perPage,
			Limit:            perPage,
		}
		if typeID != "" && typeID != "all" {
			query.DocumentTypeIDs = []string{typeID}
		}
		if corrID != "" && corrID != "all" {
			query.CorrespondentIDs = []string{corrID}
		}

		result, err := idx.Search(query)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, err.Error())
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

func handleSearchReindex(app core.App, idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if idx == nil {
			return writeError(e, http.StatusServiceUnavailable, "Search index is not ready.")
		}
		n, err := idx.Rebuild(app)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, err.Error())
		}
		return writeJSON(e, http.StatusOK, map[string]any{"indexed": n})
	}
}

type documentLookup interface {
	FindRecordById(collectionNameOrId any, recordId string, optFilters ...func(*dbx.SelectQuery) error) (*core.Record, error)
	ExpandRecord(record *core.Record, expands []string, optFetchFunc core.ExpandFetchFunc) map[string]error
}

func hydrateDocumentExports(app documentLookup, hits []fulltext.Hit, userID string) []map[string]any {
	items := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		rec, err := app.FindRecordById("documents", hit.ID)
		if err != nil {
			continue
		}
		if userID != "" && rec.GetString("user") != userID {
			continue
		}
		_ = app.ExpandRecord(rec, []string{"tags", "document_type", "correspondent"}, nil)
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
