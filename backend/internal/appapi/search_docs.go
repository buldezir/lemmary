package appapi

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/strutil"
)

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 20
	maxSummaryLen      = 300
	maxSnippetLen      = 220
	snippetContext     = 80

	// minReadCharsPerDocument keeps a read of many documents at once from
	// returning slices too short to carry a figure and its label. Bytes, like
	// the research budget it is spent against.
	minReadCharsPerDocument = 500
)

func searchUserDocuments(app core.App, idx *fulltext.Index, userID string, args ai.SearchDocumentsArgs) ([]ai.DocumentHit, error) {
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if idx == nil || !idx.Ready() {
		return nil, fmt.Errorf("search index is not ready")
	}

	limit := args.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}

	ftQuery := fulltext.Query{
		Text:     query,
		UserID:   userID,
		DateFrom: strings.TrimSpace(args.DateFrom),
		DateTo:   strings.TrimSpace(args.DateTo),
		Limit:    limit,
	}

	if typeName := strings.TrimSpace(args.DocumentType); typeName != "" {
		typeIDs, err := findNamedEntityIDs(app, "document_types", typeName, userID)
		if err != nil {
			return nil, err
		}
		if len(typeIDs) == 0 {
			return []ai.DocumentHit{}, nil
		}
		ftQuery.DocumentTypeIDs = typeIDs
	}

	if corrName := strings.TrimSpace(args.Correspondent); corrName != "" {
		corrIDs, err := findNamedEntityIDs(app, "correspondents", corrName, userID)
		if err != nil {
			return nil, err
		}
		if len(corrIDs) == 0 {
			return []ai.DocumentHit{}, nil
		}
		ftQuery.CorrespondentIDs = corrIDs
	}

	if tagNames := normalizeTagNames(args.Tags); len(tagNames) > 0 {
		tagIDs, err := findTagIDsByNames(app, tagNames, userID)
		if err != nil {
			return nil, err
		}
		if len(tagIDs) == 0 {
			return []ai.DocumentHit{}, nil
		}
		ftQuery.TagIDs = tagIDs
	}

	result, err := idx.Search(ftQuery)
	if err != nil {
		return nil, fmt.Errorf("search documents: %w", err)
	}

	hits := make([]ai.DocumentHit, 0, len(result.Hits))
	for _, bleveHit := range result.Hits {
		record, err := app.FindRecordById("documents", bleveHit.ID)
		if err != nil {
			continue
		}
		if userID != "" && record.GetString("user") != userID {
			continue
		}

		snippet := bleveHit.OCRSnippet
		if snippet == "" {
			snippet = ocrSnippet(record.GetString("ocr_text"), query)
		}

		hit := ai.DocumentHit{
			ID:           record.Id,
			Title:        strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
			DocumentDate: truncateDate(record.GetString("document_date")),
			Summary:      strutil.TruncateRunes(strutil.FirstNonEmpty(record.GetString("summary"), record.GetString("purpose")), maxSummaryLen),
			OCRSnippet:   snippet,
		}

		hit.DocumentType = relatedName(app, "document_types", record.GetString("document_type"))
		hit.Correspondent = relatedName(app, "correspondents", record.GetString("correspondent"))
		hit.Tags = documentTagNames(app, record)

		hits = append(hits, hit)
	}
	return hits, nil
}

// relatedName resolves a relation id to its display name, tolerating a missing
// or deleted record the way the hit hydration always has.
func relatedName(app documentLookup, collection, id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	record, err := app.FindRecordById(collection, id)
	if err != nil {
		return ""
	}
	return record.GetString("name")
}

func documentTagNames(app documentLookup, record *core.Record) []string {
	names := []string{}
	for _, tagID := range record.GetStringSlice("tags") {
		if tagID == "" {
			continue
		}
		tagRec, err := app.FindRecordById("tags", tagID)
		if err != nil {
			continue
		}
		if name := strings.TrimSpace(tagRec.GetString("name")); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// readUserDocuments backs the agent's read_documents tool: full extracted text
// for documents the caller owns, divided across maxTotalChars so a research run
// cannot overflow the model context window.
//
// ocr_text holds up to models.MaxOCRTextRunes runes, so truncation here is not
// an edge case — it is the normal path for anything longer than a few pages.
func readUserDocuments(app documentLookup, userID string, ids []string, maxTotalChars int) ([]ai.DocumentContent, error) {
	if len(ids) == 0 {
		return []ai.DocumentContent{}, nil
	}
	if maxTotalChars <= 0 {
		return nil, fmt.Errorf("no context budget left to read documents")
	}

	perDoc := maxTotalChars / len(ids)
	if perDoc < minReadCharsPerDocument {
		perDoc = minReadCharsPerDocument
	}

	docs := make([]ai.DocumentContent, 0, len(ids))
	spent := 0
	for _, id := range ids {
		if spent >= maxTotalChars {
			break
		}
		record, err := app.FindRecordById("documents", id)
		if err != nil {
			continue
		}
		// Re-check ownership per record. The agent only passes ids it saw from
		// search_documents, but this is the boundary that has to hold.
		if userID != "" && record.GetString("user") != userID {
			continue
		}

		text := strings.TrimSpace(record.GetString("ocr_text"))
		limit := perDoc
		if remaining := maxTotalChars - spent; limit > remaining {
			limit = remaining
		}
		// Bytes, not runes: maxTotalChars comes from the research loop's
		// budget, which counts len() everywhere. Truncating to `limit` *runes*
		// hands back up to four times that many bytes — for Cyrillic or Greek
		// OCR, reliably twice — so the reader would overshoot the window the
		// caller had just reserved for it.
		truncated := false
		if len(text) > limit {
			text = strutil.Truncate(text, limit)
			truncated = true
		}
		spent += len(text)

		docs = append(docs, ai.DocumentContent{
			ID:            record.Id,
			Title:         strutil.FirstNonEmpty(record.GetString("title"), "Untitled document"),
			DocumentDate:  truncateDate(record.GetString("document_date")),
			DocumentType:  relatedName(app, "document_types", record.GetString("document_type")),
			Correspondent: relatedName(app, "correspondents", record.GetString("correspondent")),
			Tags:          documentTagNames(app, record),
			Text:          text,
			Truncated:     truncated,
		})
	}
	return docs, nil
}

func findNamedEntityIDs(app core.App, collection, name, userID string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, nil
	}
	filter := "name ~ {:name} || name_original ~ {:name}"
	params := dbx.Params{"name": name}
	if userID != "" {
		filter = "user = {:userId} && (" + filter + ")"
		params["userId"] = userID
	}
	records, err := app.FindRecordsByFilter(
		collection,
		filter,
		"name",
		20,
		0,
		params,
	)
	if err != nil {
		return nil, fmt.Errorf("lookup %s: %w", collection, err)
	}
	ids := make([]string, 0, len(records))
	for _, record := range records {
		ids = append(ids, record.Id)
	}
	return ids, nil
}

// findTagIDsByNames resolves agent-supplied tag names to ids, scoped to
// userID (empty means unscoped, for superusers).
func findTagIDsByNames(app core.App, names []string, userID string) ([]string, error) {
	ids := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		records, err := findTagsByNameFilter(app, "name = {:name}", name, userID)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			// Fall back to substring match so near-exact agent inputs still work.
			records, err = findTagsByNameFilter(app, "name ~ {:name}", name, userID)
			if err != nil {
				return nil, err
			}
		}
		for _, record := range records {
			if _, ok := seen[record.Id]; ok {
				continue
			}
			seen[record.Id] = struct{}{}
			ids = append(ids, record.Id)
		}
	}
	return ids, nil
}

func findTagsByNameFilter(app core.App, filter, name, userID string) ([]*core.Record, error) {
	params := dbx.Params{"name": name}
	if userID != "" {
		filter = "user = {:userId} && (" + filter + ")"
		params["userId"] = userID
	}
	records, err := app.FindRecordsByFilter("tags", filter, "name", 5, 0, params)
	if err != nil {
		return nil, fmt.Errorf("lookup tags: %w", err)
	}
	return records, nil
}

func normalizeTagNames(names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out
}

func ocrSnippet(ocrText, query string) string {
	ocrText = strings.TrimSpace(ocrText)
	if ocrText == "" {
		return ""
	}
	lowerOCR := strings.ToLower(ocrText)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	idx := -1
	if lowerQuery != "" {
		idx = strings.Index(lowerOCR, lowerQuery)
	}
	if idx < 0 {
		return strutil.TruncateRunes(ocrText, maxSnippetLen)
	}

	start := idx - snippetContext
	if start < 0 {
		start = 0
	}
	// Align to rune boundaries roughly by walking back if mid-rune.
	for start > 0 && !utf8.RuneStart(ocrText[start]) {
		start--
	}
	end := idx + len(query) + snippetContext
	if end > len(ocrText) {
		end = len(ocrText)
	}
	for end < len(ocrText) && !utf8.RuneStart(ocrText[end]) {
		end++
	}

	snippet := strings.TrimSpace(ocrText[start:end])
	if start > 0 {
		snippet = "…" + snippet
	}
	if end < len(ocrText) {
		snippet += "…"
	}
	return strutil.TruncateRunes(snippet, maxSnippetLen)
}

func truncateDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 10 {
		return v[:10]
	}
	return v
}
