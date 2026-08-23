package fulltext

import (
	"fmt"
	"html"
	"strings"
	"time"
	"unicode"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
)

const (
	defaultSearchLimit = 10
	// MaxSearchLimit is the largest page size Search will return. Callers must
	// use this cap when computing offsets so pages do not skip hits.
	MaxSearchLimit = 500
)

type Query struct {
	Text             string
	UserID           string
	ProcessingStatus string
	DocumentTypeIDs  []string
	CorrespondentIDs []string
	TagIDs           []string
	DateFrom         string
	DateTo           string
	Offset           int
	Limit            int
}

type Hit struct {
	ID         string
	Score      float64
	OCRSnippet string
}

type Result struct {
	Hits  []Hit
	Total uint64
}

type queryPart struct {
	text   string
	phrase bool
}

var boostedTextFields = []struct {
	field string
	boost float64
}{
	{FieldTitle, 4},
	{FieldTitleOriginal, 4},
	{FieldTagNames, 3},
	{FieldDocumentTypeName, 3},
	{FieldCorrespondentName, 3},
	{FieldPurpose, 2},
	{FieldPurposeOriginal, 2},
	{FieldSummary, 2},
	{FieldSummaryOriginal, 2},
	{FieldPeople, 2},
	{FieldOCRText, 1},
}

func (i *Index) Search(q Query) (Result, error) {
	i.WaitIdle()
	empty := Result{Hits: []Hit{}}
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return empty, nil
	}

	limit := q.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}

	bq, err := buildQuery(q, text)
	if err != nil {
		return empty, err
	}

	var result Result
	err = i.withIndex(func(b bleve.Index) error {
		req := bleve.NewSearchRequestOptions(bq, limit, offset, false)
		req.Highlight = bleve.NewHighlight()
		req.Highlight.Fields = []string{FieldOCRText}

		res, err := b.Search(req)
		if err != nil {
			return fmt.Errorf("bleve search: %w", err)
		}

		hits := make([]Hit, 0, len(res.Hits))
		for _, h := range res.Hits {
			hit := Hit{ID: h.ID, Score: h.Score}
			if frags := h.Fragments[FieldOCRText]; len(frags) > 0 {
				hit.OCRSnippet = plainFragment(frags[0])
			}
			hits = append(hits, hit)
		}
		result = Result{Hits: hits, Total: res.Total}
		return nil
	})
	if err != nil {
		return empty, err
	}
	return result, nil
}

func (i *Index) IDsByKeyword(field, value string) ([]string, error) {
	value = strings.TrimSpace(value)
	if field == "" || value == "" {
		return nil, nil
	}

	page := lookupPageSize
	if page <= 0 {
		page = defaultLookupPage
	}

	var ids []string
	err := i.withIndex(func(b bleve.Index) error {
		tq := bleve.NewTermQuery(value)
		tq.SetField(field)
		offset := 0
		for {
			req := bleve.NewSearchRequestOptions(tq, page, offset, false)
			res, err := b.Search(req)
			if err != nil {
				return err
			}
			for _, h := range res.Hits {
				ids = append(ids, h.ID)
			}
			if len(res.Hits) < page {
				return nil
			}
			offset += page
		}
	})
	return ids, err
}

func buildQuery(q Query, text string) (query.Query, error) {
	parts := parseQueryParts(text)
	if len(parts) == 0 {
		return nil, fmt.Errorf("query has no searchable terms")
	}

	conjuncts := make([]query.Query, 0, 8)
	conjuncts = append(conjuncts, textQuery(parts))

	if userID := strings.TrimSpace(q.UserID); userID != "" {
		conjuncts = append(conjuncts, termQuery(FieldUser, userID))
	}
	if status := strings.TrimSpace(q.ProcessingStatus); status != "" && status != "all" {
		conjuncts = append(conjuncts, termQuery(FieldProcessingStatus, status))
	}
	if idQuery := anyTermQuery(FieldDocumentType, q.DocumentTypeIDs); idQuery != nil {
		conjuncts = append(conjuncts, idQuery)
	}
	if idQuery := anyTermQuery(FieldCorrespondent, q.CorrespondentIDs); idQuery != nil {
		conjuncts = append(conjuncts, idQuery)
	}
	if idQuery := anyTermQuery(FieldTags, q.TagIDs); idQuery != nil {
		conjuncts = append(conjuncts, idQuery)
	}
	if dateQuery := dateRangeQuery(q.DateFrom, q.DateTo); dateQuery != nil {
		conjuncts = append(conjuncts, dateQuery)
	}

	if len(conjuncts) == 1 {
		return conjuncts[0], nil
	}
	return bleve.NewConjunctionQuery(conjuncts...), nil
}

func textQuery(parts []queryPart) query.Query {
	termQueries := make([]query.Query, 0, len(parts))
	for _, part := range parts {
		disjuncts := make([]query.Query, 0, len(boostedTextFields))
		for _, f := range boostedTextFields {
			var mq query.Query
			if part.phrase {
				pq := bleve.NewMatchPhraseQuery(part.text)
				pq.SetField(f.field)
				pq.Analyzer = AnalyzerName
				pq.SetBoost(f.boost)
				mq = pq
			} else {
				tq := bleve.NewMatchQuery(part.text)
				tq.SetField(f.field)
				tq.Analyzer = AnalyzerName
				tq.SetBoost(f.boost)
				mq = tq
			}
			disjuncts = append(disjuncts, mq)
		}
		termQueries = append(termQueries, bleve.NewDisjunctionQuery(disjuncts...))
	}
	if len(termQueries) == 1 {
		return termQueries[0]
	}
	return bleve.NewConjunctionQuery(termQueries...)
}

func termQuery(field, value string) *query.TermQuery {
	tq := bleve.NewTermQuery(value)
	tq.SetField(field)
	return tq
}

func anyTermQuery(field string, values []string) query.Query {
	seen := map[string]struct{}{}
	queries := make([]query.Query, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		queries = append(queries, termQuery(field, v))
	}
	switch len(queries) {
	case 0:
		return nil
	case 1:
		return queries[0]
	default:
		return bleve.NewDisjunctionQuery(queries...)
	}
}

func dateRangeQuery(dateFrom, dateTo string) query.Query {
	from, fromOK := parseDayBoundary(dateFrom, false)
	to, toOK := parseDayBoundary(dateTo, true)
	if !fromOK && !toOK {
		return nil
	}
	startInc := true
	endInc := false
	var start, end time.Time
	var startP, endP *bool
	if fromOK {
		start = from
		startP = &startInc
	}
	if toOK {
		end = to
		endP = &endInc
	}
	dq := bleve.NewDateRangeInclusiveQuery(start, end, startP, endP)
	dq.SetField(FieldDocumentDate)
	return dq
}

func parseDayBoundary(s string, endExclusive bool) (time.Time, bool) {
	t, ok := parseDocumentDate(s)
	if !ok {
		return time.Time{}, false
	}
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	if endExclusive {
		return day.Add(24 * time.Hour), true
	}
	return day, true
}

func parseQueryParts(q string) []queryPart {
	var parts []queryPart
	var buf strings.Builder
	inQuote := false
	flush := func(phrase bool) {
		s := strings.TrimSpace(buf.String())
		buf.Reset()
		if s != "" {
			parts = append(parts, queryPart{text: s, phrase: phrase})
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			if inQuote {
				flush(true)
				inQuote = false
			} else {
				flush(false)
				inQuote = true
			}
		case unicode.IsSpace(r) && !inQuote:
			flush(false)
		default:
			buf.WriteRune(r)
		}
	}
	flush(inQuote)
	return parts
}

func plainFragment(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	s = strings.ReplaceAll(s, "</mark>", "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}
