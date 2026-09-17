package appapi

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/strutil"
)

const (
	mcpDefaultListLimit = 50
	mcpMaxListLimit     = 200
	// mcpDefaultTextChars is how much of a document one get_document call
	// returns when the caller does not say; the rest is paged by offset.
	mcpDefaultTextChars = 100_000
)

// mcpDocument is the metadata an agent gets for one document, with or
// without its text.
type mcpDocument struct {
	ID               string   `json:"id"`
	Title            string   `json:"title"`
	DocumentDate     string   `json:"document_date,omitempty"`
	DocumentType     string   `json:"document_type,omitempty"`
	Correspondent    string   `json:"correspondent,omitempty"`
	Tags             []string `json:"tags"`
	Summary          string   `json:"summary,omitempty"`
	ProcessingStatus string   `json:"processing_status"`
	PageCount        int      `json:"page_count,omitempty"`
	SizeBytes        int      `json:"size_bytes,omitempty"`
	Created          string   `json:"created"`
	Updated          string   `json:"updated"`
}

type mcpListArgs struct {
	DateFrom      string   `json:"date_from,omitempty" jsonschema:"Inclusive lower bound on document_date, YYYY-MM-DD."`
	DateTo        string   `json:"date_to,omitempty" jsonschema:"Inclusive upper bound on document_date, YYYY-MM-DD."`
	DocumentType  string   `json:"document_type,omitempty" jsonschema:"Document type name filter (substring match)."`
	Correspondent string   `json:"correspondent,omitempty" jsonschema:"Correspondent name filter (substring match)."`
	Tags          []string `json:"tags,omitempty" jsonschema:"Exact tag names; documents with any of them match."`
	Status        string   `json:"status,omitempty" jsonschema:"processing_status filter: pending, processing, completed, failed, cancelled or needs_review."`
	Sort          string   `json:"sort,omitempty" jsonschema:"One of date_desc (default), date_asc, added_desc, added_asc."`
	Limit         int      `json:"limit,omitempty" jsonschema:"Page size, 1-200, default 50."`
	Offset        int      `json:"offset,omitempty" jsonschema:"Documents to skip, for paging."`
}

type mcpListResult struct {
	Documents []mcpDocument `json:"documents"`
	// Total is how many match the filters, not how many this page holds.
	Total int `json:"total"`
	// Unresolved names filter values that matched nothing; the list is
	// empty then because there is no such thing, not because there are none.
	Unresolved []string `json:"unresolved_filters,omitempty"`
}

type mcpGetArgs struct {
	ID       string `json:"id" jsonschema:"Document id."`
	Offset   int    `json:"offset,omitempty" jsonschema:"Characters of text to skip, for paging through a long document."`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Most characters of text to return, default 100000."`
}

type mcpGetResult struct {
	mcpDocument
	Text string `json:"text"`
	// TextChars is the whole document's length, so a caller can tell a
	// complete read from a page of one.
	TextChars int  `json:"text_chars"`
	Truncated bool `json:"truncated,omitempty"`
}

type mcpTaxonomyResult struct {
	Tags           []string `json:"tags"`
	DocumentTypes  []string `json:"document_types"`
	Correspondents []string `json:"correspondents"`
}

// mcpDocs is plain access to the archive: no ranking, no excerpting, just the
// rows and the text, scoped to one user (empty means unscoped).
type mcpDocs struct {
	list     func(ctx context.Context, args mcpListArgs) (mcpListResult, error)
	get      func(ctx context.Context, args mcpGetArgs) (mcpGetResult, error)
	taxonomy func(ctx context.Context) (mcpTaxonomyResult, error)
}

func newMCPDocs(app core.App, userID string) mcpDocs {
	retriever := &agentRetriever{app: app, userID: userID}
	return mcpDocs{
		list: func(ctx context.Context, args mcpListArgs) (mcpListResult, error) {
			return listMCPDocuments(ctx, app, retriever, args)
		},
		get: func(_ context.Context, args mcpGetArgs) (mcpGetResult, error) {
			return getMCPDocument(app, userID, args)
		},
		taxonomy: func(context.Context) (mcpTaxonomyResult, error) {
			out := mcpTaxonomyResult{}
			var err error
			if out.Tags, err = listNames(app, "tags", userID); err != nil {
				return out, err
			}
			if out.DocumentTypes, err = listNames(app, "document_types", userID); err != nil {
				return out, err
			}
			out.Correspondents, err = listNames(app, "correspondents", userID)
			return out, err
		},
	}
}

func listMCPDocuments(ctx context.Context, app core.App, retriever *agentRetriever, args mcpListArgs) (mcpListResult, error) {
	ftQuery, unresolved, err := retriever.resolveFilters(ai.SearchDocumentsArgs{
		DateFrom:      args.DateFrom,
		DateTo:        args.DateTo,
		DocumentType:  args.DocumentType,
		Correspondent: args.Correspondent,
		Tags:          args.Tags,
	})
	if err != nil {
		return mcpListResult{}, err
	}
	result := mcpListResult{Documents: []mcpDocument{}, Unresolved: unresolved}
	if len(unresolved) > 0 {
		return result, nil
	}
	order, err := mcpListOrder(args.Sort)
	if err != nil {
		return mcpListResult{}, err
	}
	limit := args.Limit
	if limit <= 0 {
		limit = mcpDefaultListLimit
	}
	if limit > mcpMaxListLimit {
		limit = mcpMaxListLimit
	}
	spec := countSpec{
		userID:           retriever.userID,
		documentTypeIDs:  ftQuery.DocumentTypeIDs,
		correspondentIDs: ftQuery.CorrespondentIDs,
		tagIDs:           ftQuery.TagIDs,
		dateFrom:         ftQuery.DateFrom,
		dateTo:           ftQuery.DateTo,
		status:           strings.ToLower(strings.TrimSpace(args.Status)),
	}
	where, params := documentConditions(spec)
	sql := `FROM documents d`
	if len(where) > 0 {
		sql += ` WHERE ` + strings.Join(where, " AND ")
	}
	db := app.DB()
	var total struct {
		Count int `db:"count"`
	}
	if err := db.NewQuery(`SELECT COUNT(*) AS count ` + sql).Bind(params).WithContext(ctx).One(&total); err != nil {
		return mcpListResult{}, fmt.Errorf("count documents: %w", err)
	}
	result.Total = total.Count
	var ids []string
	err = db.NewQuery(`SELECT d.id ` + sql + ` ORDER BY ` + order + ` LIMIT ` + fmt.Sprint(limit) + ` OFFSET ` + fmt.Sprint(max(args.Offset, 0))).
		Bind(params).WithContext(ctx).Column(&ids)
	if err != nil {
		return mcpListResult{}, fmt.Errorf("list documents: %w", err)
	}
	for _, id := range ids {
		record, err := app.FindRecordById("documents", id)
		if err != nil {
			continue
		}
		result.Documents = append(result.Documents, mcpDocumentOf(app, record))
	}
	return result, nil
}

// mcpListOrder maps the sort name to SQL. An undated document sorts by the
// day it was added, which is the date the app shows for it.
func mcpListOrder(sort string) (string, error) {
	const date = `COALESCE(NULLIF(substr(d.document_date, 1, 10), ''), substr(d.created, 1, 10))`
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case "", "date_desc":
		return date + ` DESC, d.created DESC`, nil
	case "date_asc":
		return date + ` ASC, d.created ASC`, nil
	case "added_desc":
		return `d.created DESC`, nil
	case "added_asc":
		return `d.created ASC`, nil
	}
	return "", fmt.Errorf("unknown sort %q; use date_desc, date_asc, added_desc or added_asc", sort)
}

func getMCPDocument(app core.App, userID string, args mcpGetArgs) (mcpGetResult, error) {
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return mcpGetResult{}, fmt.Errorf("id is required")
	}
	record, err := app.FindRecordById("documents", id)
	if err != nil || (userID != "" && record.GetString("user") != userID) {
		return mcpGetResult{}, fmt.Errorf("no document %s", id)
	}
	text := record.GetString("ocr_text")
	out := mcpGetResult{mcpDocument: mcpDocumentOf(app, record), TextChars: utf8.RuneCountInString(text)}
	maxChars := args.MaxChars
	if maxChars <= 0 {
		maxChars = mcpDefaultTextChars
	}
	runes := []rune(text)
	start := min(max(args.Offset, 0), len(runes))
	end := min(start+maxChars, len(runes))
	out.Text = string(runes[start:end])
	out.Truncated = start > 0 || end < len(runes)
	return out, nil
}

func mcpDocumentOf(app core.App, record *core.Record) mcpDocument {
	return mcpDocument{
		ID:               record.Id,
		Title:            strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
		DocumentDate:     truncateDate(record.GetString("document_date")),
		DocumentType:     relatedName(app, "document_types", record.GetString("document_type")),
		Correspondent:    relatedName(app, "correspondents", record.GetString("correspondent")),
		Tags:             documentTagNames(app, record),
		Summary:          record.GetString("summary"),
		ProcessingStatus: record.GetString("processing_status"),
		PageCount:        record.GetInt("page_count"),
		SizeBytes:        record.GetInt("size_bytes"),
		Created:          record.GetString("created"),
		Updated:          record.GetString("updated"),
	}
}
