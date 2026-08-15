package appapi

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/ai"
	"paperless-go/backend/internal/fulltext"
)

const (
	defaultSearchLimit = 10
	maxSearchLimit     = 20
	maxSummaryLen      = 300
	maxSnippetLen      = 220
	snippetContext     = 80
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
		tagIDs, err := findTagIDsByNames(app, tagNames)
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
			Title:        firstNonEmpty(record.GetString("title"), "Untitled document"),
			DocumentDate: truncateDate(record.GetString("document_date")),
			Summary:      truncateRunes(firstNonEmpty(record.GetString("summary"), record.GetString("purpose")), maxSummaryLen),
			OCRSnippet:   snippet,
			Tags:         []string{},
		}

		if typeID := record.GetString("document_type"); typeID != "" {
			if typeRec, err := app.FindRecordById("document_types", typeID); err == nil {
				hit.DocumentType = typeRec.GetString("name")
			}
		}
		if corrID := record.GetString("correspondent"); corrID != "" {
			if corrRec, err := app.FindRecordById("correspondents", corrID); err == nil {
				hit.Correspondent = corrRec.GetString("name")
			}
		}
		for _, tagID := range record.GetStringSlice("tags") {
			if tagID == "" {
				continue
			}
			tagRec, err := app.FindRecordById("tags", tagID)
			if err != nil {
				continue
			}
			if name := strings.TrimSpace(tagRec.GetString("name")); name != "" {
				hit.Tags = append(hit.Tags, name)
			}
		}

		hits = append(hits, hit)
	}
	return hits, nil
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

func findTagIDsByNames(app core.App, names []string) ([]string, error) {
	ids := make([]string, 0, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		records, err := app.FindRecordsByFilter(
			"tags",
			"name = {:name}",
			"name",
			5,
			0,
			dbx.Params{"name": name},
		)
		if err != nil {
			return nil, fmt.Errorf("lookup tags: %w", err)
		}
		if len(records) == 0 {
			// Fall back to substring match so near-exact agent inputs still work.
			records, err = app.FindRecordsByFilter(
				"tags",
				"name ~ {:name}",
				"name",
				5,
				0,
				dbx.Params{"name": name},
			)
			if err != nil {
				return nil, fmt.Errorf("lookup tags: %w", err)
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
		return truncateRunes(ocrText, maxSnippetLen)
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
	return truncateRunes(snippet, maxSnippetLen)
}

func truncateDate(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 10 {
		return v[:10]
	}
	return v
}

func truncateRunes(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || s == "" {
		return s
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
