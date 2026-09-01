package fulltext

import (
	"fmt"
	"html"
	"math"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	// Relaxed trades precision for recall: unquoted terms no longer all have
	// to match, and long words also match with one edit of slack.
	//
	// Off by default, and the Documents page leaves it off deliberately. There
	// the query box is a filter — the user types words they know are in the
	// document and expects the list to shrink to exactly those — so a result
	// that matched two words out of three would read as a bug. The agent's
	// tools turn it on: there the query is a guess the model made from a
	// question, and a near miss is worth far more than an empty list.
	Relaxed bool
}

type Hit struct {
	ID         string
	Score      float64
	OCRSnippet string
	// OCRFragments is every highlight fragment Bleve produced for the OCR
	// text, best first. OCRSnippet is the first of them; the agent path quotes
	// several, because one fragment out of a ten-page document is a hint about
	// where the answer is rather than the answer.
	OCRFragments []string
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
			for _, frag := range h.Fragments[FieldOCRText] {
				if plain := plainFragment(frag); plain != "" {
					hit.OCRFragments = append(hit.OCRFragments, plain)
				}
			}
			if len(hit.OCRFragments) > 0 {
				hit.OCRSnippet = hit.OCRFragments[0]
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

	var ids []string
	err := i.withIndex(func(b bleve.Index) error {
		var err error
		ids, err = idsByKeyword(b, field, value)
		return err
	})
	return ids, err
}

// EligibleIDs lists the documents that satisfy everything in q except its text,
// up to limit. complete is false when there were more, which is the caller's
// signal that the list cannot be used as a pre-filter.
//
// This is how a filtered agent search reaches the chunk index: the filters are
// document properties the chunk index deliberately does not carry, so they are
// resolved here and passed down as ids.
func (i *Index) EligibleIDs(q Query, limit int) ([]string, bool, error) {
	filter := filterQuery(q)
	if filter == nil || limit <= 0 {
		return nil, true, nil
	}

	var (
		ids      []string
		complete bool
	)
	err := i.withIndex(func(b bleve.Index) error {
		// One over the limit, so a full page is distinguishable from a page
		// that happened to end exactly there.
		req := bleve.NewSearchRequestOptions(filter, limit+1, 0, false)
		res, err := b.Search(req)
		if err != nil {
			return err
		}
		complete = len(res.Hits) <= limit
		for n, h := range res.Hits {
			if n == limit {
				break
			}
			ids = append(ids, h.ID)
		}
		return nil
	})
	return ids, complete, err
}

// KeepEligible returns the subset of ids that satisfies q's filters. The
// post-filter for the case EligibleIDs could not pre-filter: the dense list is
// short, so it is cheaper to ask about its documents than to enumerate every
// document the filters allow.
func (i *Index) KeepEligible(q Query, ids []string) ([]string, error) {
	filter := filterQuery(q)
	if filter == nil || len(ids) == 0 {
		return ids, nil
	}

	var kept []string
	err := i.withIndex(func(b bleve.Index) error {
		bq := bleve.NewConjunctionQuery(filter, bleve.NewDocIDQuery(ids))
		req := bleve.NewSearchRequestOptions(bq, len(ids), 0, false)
		res, err := b.Search(req)
		if err != nil {
			return err
		}
		allowed := make(map[string]struct{}, len(res.Hits))
		for _, h := range res.Hits {
			allowed[h.ID] = struct{}{}
		}
		kept = make([]string, 0, len(allowed))
		for _, id := range ids {
			if _, ok := allowed[id]; ok {
				kept = append(kept, id)
			}
		}
		return nil
	})
	return kept, err
}

func buildQuery(q Query, text string) (query.Query, error) {
	parts := parseQueryParts(text)
	if len(parts) == 0 {
		return nil, fmt.Errorf("query has no searchable terms")
	}

	conjuncts := make([]query.Query, 0, 8)
	conjuncts = append(conjuncts, textQuery(parts, q.Relaxed))
	conjuncts = append(conjuncts, filterConjuncts(q)...)

	if len(conjuncts) == 1 {
		return conjuncts[0], nil
	}
	return bleve.NewConjunctionQuery(conjuncts...), nil
}

// filterConjuncts is everything in a Query except its text: ownership, status,
// the named-entity ids and the date range.
func filterConjuncts(q Query) []query.Query {
	conjuncts := make([]query.Query, 0, 6)
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
	return conjuncts
}

// filterQuery is filterConjuncts as one query, or nil when a query filters
// nothing.
func filterQuery(q Query) query.Query {
	conjuncts := filterConjuncts(q)
	switch len(conjuncts) {
	case 0:
		return nil
	case 1:
		return conjuncts[0]
	default:
		return bleve.NewConjunctionQuery(conjuncts...)
	}
}

// HasDocumentFilters reports whether q restricts the result set by anything
// other than its owner — the filters the chunk index cannot apply itself.
func HasDocumentFilters(q Query) bool {
	bare := q
	bare.UserID = ""
	return len(filterConjuncts(bare)) > 0
}

func textQuery(parts []queryPart, relaxed bool) query.Query {
	if !relaxed {
		termQueries := make([]query.Query, 0, len(parts))
		for _, part := range parts {
			termQueries = append(termQueries, fieldQuery(part, false))
		}
		if len(termQueries) == 1 {
			return termQueries[0]
		}
		return bleve.NewConjunctionQuery(termQueries...)
	}

	// A quoted phrase is an instruction, not a guess: the user (or the model)
	// asked for those words in that order, so it stays a mandatory conjunct
	// however many loose terms surround it.
	must := make([]query.Query, 0, len(parts))
	should := make([]query.Query, 0, len(parts))
	for _, part := range parts {
		if part.phrase {
			must = append(must, fieldQuery(part, false))
			continue
		}
		should = append(should, fieldQuery(part, true))
	}
	if len(should) > 0 {
		dq := bleve.NewDisjunctionQuery(should...)
		dq.SetMin(float64(minShouldMatch(len(should))))
		must = append(must, dq)
	}
	if len(must) == 1 {
		return must[0]
	}
	return bleve.NewConjunctionQuery(must...)
}

// fieldQuery matches one query part across every searchable field, at that
// field's boost. With fuzzy on, a long word also matches with one edit of
// slack at half the boost, so an exact match always outranks an approximate
// one rather than merely appearing beside it.
func fieldQuery(part queryPart, fuzzy bool) query.Query {
	disjuncts := make([]query.Query, 0, 2*len(boostedTextFields))
	for _, f := range boostedTextFields {
		if part.phrase {
			pq := bleve.NewMatchPhraseQuery(part.text)
			pq.SetField(f.field)
			pq.Analyzer = AnalyzerName
			pq.SetBoost(f.boost)
			disjuncts = append(disjuncts, pq)
			continue
		}
		tq := bleve.NewMatchQuery(part.text)
		tq.SetField(f.field)
		tq.Analyzer = AnalyzerName
		tq.SetBoost(f.boost)
		disjuncts = append(disjuncts, tq)

		if !fuzzy || !fuzzyWorthy(part.text) {
			continue
		}
		fq := bleve.NewMatchQuery(part.text)
		fq.SetField(f.field)
		fq.Analyzer = AnalyzerName
		fq.SetFuzziness(1)
		fq.SetPrefix(1)
		fq.SetBoost(f.boost * 0.5)
		disjuncts = append(disjuncts, fq)
	}
	return bleve.NewDisjunctionQuery(disjuncts...)
}

// fuzzyWorthy decides which terms get an edit of slack. Short words are out
// because one edit reaches most of the dictionary from them, and anything with
// a digit is out because it is an id, an amount or a date — the values where a
// near miss is a different document, not the same one spelled badly.
func fuzzyWorthy(term string) bool {
	if utf8.RuneCountInString(term) < 5 {
		return false
	}
	for _, r := range term {
		if unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// minShouldMatch is how many of n loose terms a document must carry.
//
// Two terms both have to match: dropping one of two leaves a single word, and
// a single word matches the archive. From three up one term may be missing —
// the model's keyword guesses are wrong often enough that insisting on all of
// them is what produces empty result lists — and past five the fraction takes
// over so a long query is not held to an all-or-nothing standard either.
func minShouldMatch(n int) int {
	switch {
	case n <= 2:
		return n
	case n <= 5:
		return n - 1
	default:
		return int(math.Ceil(0.7 * float64(n)))
	}
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
