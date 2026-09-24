package appapi

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/strutil"
)

const (
	mcpDefaultListLimit = 50
	mcpMaxListLimit     = 200
	// mcpDefaultTextChars is how much of a document one get_document call
	// returns when the caller does not say; mcpMaxTextChars is the most it may
	// ask for. The rest is paged by offset.
	mcpDefaultTextChars = 100_000
	mcpMaxTextChars     = 200_000
	// mcpMaxTaxonomyNames bounds one list_taxonomy answer per collection. Ten
	// times the in-app agent's prompt budget, because an agent filtering by
	// name needs the whole vocabulary, not a representative slice.
	mcpMaxTaxonomyNames = 5000
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
	Status        string   `json:"status,omitempty" jsonschema:"processing_status filter: pending, processing, completed, failed, cancelled, needs_review, or unfinished for everything but completed."`
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
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Most characters of text to return: default 100000, at most 200000."`
}

type mcpGetResult struct {
	mcpDocument
	Text string `json:"text"`
	// TextChars is the whole document's length, so a caller can tell a
	// complete read from a page of one.
	TextChars int `json:"text_chars"`
	// Truncated means text remains after this page.
	Truncated bool `json:"truncated,omitempty"`
}

type mcpTaxonomyResult struct {
	Tags           []string `json:"tags"`
	DocumentTypes  []string `json:"document_types"`
	Correspondents []string `json:"correspondents"`
	// Truncated marks a list cut at the per-collection cap.
	Truncated bool `json:"truncated,omitempty"`
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
			for _, part := range []struct {
				collection string
				into       *[]string
			}{
				{"tags", &out.Tags},
				{"document_types", &out.DocumentTypes},
				{"correspondents", &out.Correspondents},
			} {
				names, truncated, err := taxonomyNames(app, part.collection, userID, mcpMaxTaxonomyNames)
				if err != nil {
					return out, err
				}
				*part.into = names
				out.Truncated = out.Truncated || truncated
			}
			return out, nil
		},
	}
}

// taxonomyNames asks for one more than limit, which is how it can tell a
// full list from one that was cut there.
func taxonomyNames(app core.App, collection, userID string, limit int) ([]string, bool, error) {
	names, err := listNames(app, collection, userID, limit+1)
	if err != nil {
		return nil, false, err
	}
	if len(names) > limit {
		return names[:limit], true, nil
	}
	return names, false, nil
}

// mcpStatuses maps the status filter to the set of statuses it means. An
// unknown one is refused rather than matched against nothing.
func mcpStatuses(status string) ([]string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	switch status {
	case "":
		return nil, nil
	case models.StatusFilterUnfinished:
		return models.UnfinishedDocStatuses, nil
	case models.DocStatusPending, models.DocStatusProcessing, models.DocStatusCompleted,
		models.DocStatusFailed, models.DocStatusCancelled, models.DocStatusNeedsReview:
		return []string{status}, nil
	}
	return nil, fmt.Errorf("unknown status %q; use pending, processing, completed, failed, cancelled, needs_review or unfinished", status)
}

func listMCPDocuments(ctx context.Context, app core.App, retriever *agentRetriever, args mcpListArgs) (mcpListResult, error) {
	order, err := mcpListOrder(args.Sort)
	if err != nil {
		return mcpListResult{}, err
	}
	statuses, err := mcpStatuses(args.Status)
	if err != nil {
		return mcpListResult{}, err
	}
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
		statuses:         statuses,
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
	err = db.NewQuery(`SELECT d.id `+sql+` ORDER BY `+order+` LIMIT `+fmt.Sprint(limit)+` OFFSET `+fmt.Sprint(max(args.Offset, 0))).
		Bind(params).WithContext(ctx).Column(&ids)
	if err != nil {
		return mcpListResult{}, fmt.Errorf("list documents: %w", err)
	}
	if len(ids) == 0 {
		return result, nil
	}
	records, err := app.FindRecordsByIds("documents", ids)
	if err != nil {
		return mcpListResult{}, fmt.Errorf("load documents: %w", err)
	}
	// One page, one fetch per relation: the query's order is kept and a row
	// deleted between the two statements is simply absent.
	expandMCPDocuments(app, records)
	byID := make(map[string]*core.Record, len(records))
	for _, record := range records {
		byID[record.Id] = record
	}
	for _, id := range ids {
		if record := byID[id]; record != nil {
			result.Documents = append(result.Documents, mcpDocumentOf(record))
		}
	}
	return result, nil
}

// mcpListOrder maps the sort name to SQL. An undated document sorts by the
// day it was added, which is the date the app shows for it. The id is the
// tiebreaker that makes paging a partition when timestamps collide.
func mcpListOrder(sort string) (string, error) {
	const date = `COALESCE(NULLIF(substr(d.document_date, 1, 10), ''), substr(d.created, 1, 10))`
	switch strings.ToLower(strings.TrimSpace(sort)) {
	case "", "date_desc":
		return date + ` DESC, d.created DESC, d.id DESC`, nil
	case "date_asc":
		return date + ` ASC, d.created ASC, d.id ASC`, nil
	case "added_desc":
		return `d.created DESC, d.id DESC`, nil
	case "added_asc":
		return `d.created ASC, d.id ASC`, nil
	}
	return "", fmt.Errorf("unknown sort %q; use date_desc, date_asc, added_desc or added_asc", sort)
}

func getMCPDocument(app core.App, userID string, args mcpGetArgs) (mcpGetResult, error) {
	id := strings.TrimSpace(args.ID)
	if id == "" {
		return mcpGetResult{}, fmt.Errorf("id is required")
	}
	record, err := app.FindRecordById("documents", id)
	if err != nil || !CanReadDocument(app, record, userID) {
		return mcpGetResult{}, fmt.Errorf("no document %s", id)
	}
	expandMCPDocuments(app, []*core.Record{record})
	text := record.GetString("ocr_text")
	out := mcpGetResult{mcpDocument: mcpDocumentOf(record), TextChars: utf8.RuneCountInString(text)}
	maxChars := args.MaxChars
	if maxChars <= 0 {
		maxChars = mcpDefaultTextChars
	}
	if maxChars > mcpMaxTextChars {
		maxChars = mcpMaxTextChars
	}
	out.Text, out.Truncated = runePage(text, max(args.Offset, 0), maxChars)
	return out, nil
}

// runePage is text[offset:offset+count] in runes, and whether any text
// follows the page. One pass over the bytes, no rune slice of the whole
// document for a page of it. count is bounded by the caller, so the sum
// cannot wrap.
func runePage(text string, offset, count int) (string, bool) {
	start := len(text)
	n := 0
	for i := range text {
		if n == offset {
			start = i
		}
		if n == offset+count {
			return text[start:i], true
		}
		n++
	}
	return text[start:], false
}

func expandMCPDocuments(app core.App, records []*core.Record) {
	_ = app.ExpandRecords(records, []string{"tags", "document_type", "correspondent"}, nil)
}

// mcpDocumentOf reads an expanded record; relations that failed to expand
// come back empty rather than as ids.
func mcpDocumentOf(record *core.Record) mcpDocument {
	tags := []string{}
	for _, tag := range record.ExpandedAll("tags") {
		if name := strings.TrimSpace(tag.GetString("name")); name != "" {
			tags = append(tags, name)
		}
	}
	slices.Sort(tags)
	return mcpDocument{
		ID:               record.Id,
		Title:            strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
		DocumentDate:     truncateDate(record.GetString("document_date")),
		DocumentType:     expandedName(record, "document_type"),
		Correspondent:    expandedName(record, "correspondent"),
		Tags:             tags,
		Summary:          record.GetString("summary"),
		ProcessingStatus: record.GetString("processing_status"),
		PageCount:        record.GetInt("page_count"),
		SizeBytes:        record.GetInt("size_bytes"),
		Created:          record.GetString("created"),
		Updated:          record.GetString("updated"),
	}
}

func expandedName(record *core.Record, field string) string {
	if related := record.ExpandedOne(field); related != nil {
		return related.GetString("name")
	}
	return ""
}
