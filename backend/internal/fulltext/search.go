package fulltext

import (
	"errors"
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
	// relaxedFallbackLimit caps the last rung of the relaxation ladder. Those
	// hits carry one keyword out of several, so a long list of them is noise the
	// caller pays for; the disjunction's coord factor has already put the best
	// coverage first.
	relaxedFallbackLimit = 10
)

// ErrNoSearchableTerms is text that is not empty but tokenises to nothing: a
// lone quote, punctuation on its own. A sentinel so a caller can answer it with
// an empty page rather than a 500.
var ErrNoSearchableTerms = errors.New("query has no searchable terms")

type Query struct {
	Text             string
	UserID           string
	ProcessingStatus string
	DocumentTypeIDs  []string
	CorrespondentIDs []string
	TagIDs           []string
	DateFrom         string
	DateTo           string
	// Fields narrows the text match to a subset of the searchable fields, by
	// their index field name. Empty means every field, which is what the
	// archive-wide search box and the agent both want; the paperless-ngx
	// compatibility layer uses it to honour title-only and content-only
	// filters, which name a field where a general query names none.
	Fields []string
	Offset int
	Limit  int
	// Relaxed trades precision for recall: unquoted terms no longer all have to
	// match.
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
	// Terms is how many unquoted terms the query text had; Required is how many
	// of them a hit had to carry. Required < Terms means every hit is a partial
	// match, and Total is then "documents matching at least Required terms" —
	// not comparable with the strict path's Total. Both zero when Relaxed is off.
	Terms    int
	Required int
}

type queryPart struct {
	text   string
	phrase bool
	// closed is false for a phrase that never got its closing quote. The strict
	// path still honours it; a relaxed query demotes it to a loose term, because
	// an unterminated quote is a typo, not an instruction.
	closed bool
}

// relaxMode is how hard a query insists on its own terms.
type relaxMode int

const (
	relaxOff  relaxMode = iota // every term mandatory (Documents page, ngxapi)
	relaxSome                  // minShouldMatch(n) of n terms
	relaxAny                   // 1 of n terms, plus a fuzzy leg per long term
)

// searchPlan is the relaxation ladder for one query: the rung to try, and the
// wider rung to fall back on when the first matches nothing at all.
type searchPlan struct {
	primary  query.Query
	fallback query.Query // nil unless relaxing further would change the query
	terms    int
	required int
}

// boostedField is one searchable field and how much a match in it counts.
type boostedField struct {
	field string
	boost float64
}

var boostedTextFields = []boostedField{
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

	plan, err := buildSearchPlan(q, text)
	if err != nil {
		return empty, err
	}

	var result Result
	// Both rungs run inside one closure so a concurrent Rebuild cannot swap the
	// index out from under the fallback.
	err = i.withIndex(func(b bleve.Index) error {
		required := plan.required
		res, err := runTier(b, plan.primary, limit, offset)
		if err != nil {
			return err
		}
		// Escalate on Total, never on len(Hits): an Offset past the end of a
		// result set empties the hit slice while Total stays nonzero, and
		// widening the query there would swap the corpus under a paging caller.
		if res.Total == 0 && plan.fallback != nil {
			res, err = runTier(b, plan.fallback, min(limit, relaxedFallbackLimit), offset)
			if err != nil {
				return err
			}
			required = 1
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
		result = Result{Hits: hits, Total: res.Total, Terms: plan.terms, Required: required}
		return nil
	})
	if err != nil {
		return empty, err
	}
	return result, nil
}

func runTier(b bleve.Index, bq query.Query, limit, offset int) (*bleve.SearchResult, error) {
	req := bleve.NewSearchRequestOptions(bq, limit, offset, false)
	req.Highlight = bleve.NewHighlight()
	req.Highlight.Fields = []string{FieldOCRText}

	res, err := b.Search(req)
	if err != nil {
		return nil, fmt.Errorf("bleve search: %w", err)
	}
	return res, nil
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

// MatchingIDs enumerates the documents matching q -- text and filters, strict
// matching -- up to limit, without highlighting. total is the full match
// count whatever the limit; complete is whether ids holds all of it.
//
// This is what a grouped count runs on: the query says which documents, the
// database says how they break down. Search itself is the wrong tool for it
// because it highlights every hit, and a count wants none of that.
func (i *Index) MatchingIDs(q Query, limit int) (ids []string, total uint64, complete bool, err error) {
	i.WaitIdle()
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return nil, 0, false, fmt.Errorf("query text is required")
	}
	if limit <= 0 {
		return nil, 0, false, nil
	}
	q.Relaxed = false
	plan, err := buildSearchPlan(q, text)
	if err != nil {
		return nil, 0, false, err
	}
	err = i.withIndex(func(b bleve.Index) error {
		offset := 0
		for len(ids) < limit {
			page := min(MaxSearchLimit, limit-len(ids))
			req := bleve.NewSearchRequestOptions(plan.primary, page, offset, false)
			res, err := b.Search(req)
			if err != nil {
				return fmt.Errorf("bleve search: %w", err)
			}
			total = res.Total
			for _, h := range res.Hits {
				ids = append(ids, h.ID)
			}
			offset += len(res.Hits)
			if len(res.Hits) == 0 || uint64(offset) >= res.Total {
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, false, err
	}
	return ids, total, uint64(len(ids)) >= total, nil
}

// CountMatching is the strict match count for q -- text and filters -- with
// nothing fetched. The text is required; a filters-only count is the
// database's job.
func (i *Index) CountMatching(q Query) (uint64, error) {
	i.WaitIdle()
	text := strings.TrimSpace(q.Text)
	if text == "" {
		return 0, fmt.Errorf("query text is required")
	}
	q.Relaxed = false
	plan, err := buildSearchPlan(q, text)
	if err != nil {
		return 0, err
	}
	var total uint64
	err = i.withIndex(func(b bleve.Index) error {
		req := bleve.NewSearchRequestOptions(plan.primary, 0, 0, false)
		res, err := b.Search(req)
		if err != nil {
			return fmt.Errorf("bleve search: %w", err)
		}
		total = res.Total
		return nil
	})
	return total, err
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

func buildSearchPlan(q Query, text string) (searchPlan, error) {
	parts := parseQueryParts(text)
	if len(parts) == 0 {
		return searchPlan{}, ErrNoSearchableTerms
	}
	filters := filterConjuncts(q)
	fields := searchFields(q.Fields)

	if !q.Relaxed {
		return searchPlan{primary: withFilters(textQuery(parts, relaxOff, fields), filters)}, nil
	}

	loose := looseParts(parts)
	plan := searchPlan{
		primary: withFilters(textQuery(parts, relaxSome, fields), filters),
		terms:   len(loose),
	}
	if len(loose) == 0 {
		// Every part is a closed phrase, so there is nothing left to relax.
		return plan, nil
	}
	plan.required = minShouldMatch(len(loose))
	// Only build the wider rung when it would actually differ. A single short
	// term is already its own floor; a single long one still earns a rung,
	// because there the fallback degenerates into a spelling-correction retry.
	if plan.required > 1 || anyFuzzyWorthy(loose) {
		plan.fallback = withFilters(textQuery(parts, relaxAny, fields), filters)
	}
	return plan, nil
}

// filterConjuncts is everything in a Query except its text: ownership, status,
// the named-entity ids and the date range. Mandatory in every relax mode.
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

func withFilters(text query.Query, filters []query.Query) query.Query {
	if len(filters) == 0 {
		return text
	}
	conjuncts := make([]query.Query, 0, len(filters)+1)
	conjuncts = append(conjuncts, text)
	conjuncts = append(conjuncts, filters...)
	return bleve.NewConjunctionQuery(conjuncts...)
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

// looseParts is the parts a relaxed query is allowed to drop: everything but a
// properly closed phrase.
func looseParts(parts []queryPart) []queryPart {
	loose := make([]queryPart, 0, len(parts))
	for _, part := range parts {
		if mandatoryPhrase(part) {
			continue
		}
		part.phrase = false
		loose = append(loose, part)
	}
	return loose
}

// mandatoryPhrase reports whether a part is a phrase the caller really asked
// for. A quoted phrase is an instruction, not a guess, so it stays a mandatory
// conjunct however many loose terms surround it — but only once it was actually
// closed, since an unterminated quote is a typo.
func mandatoryPhrase(part queryPart) bool {
	return part.phrase && part.closed
}

func textQuery(parts []queryPart, mode relaxMode, fields []boostedField) query.Query {
	if mode == relaxOff {
		conjuncts := make([]query.Query, 0, len(parts))
		for _, part := range parts {
			conjuncts = append(conjuncts, fieldQuery(part, false, fields))
		}
		if len(conjuncts) == 1 {
			return conjuncts[0]
		}
		return bleve.NewConjunctionQuery(conjuncts...)
	}

	must := make([]query.Query, 0, len(parts))
	for _, part := range parts {
		if mandatoryPhrase(part) {
			must = append(must, fieldQuery(part, false, fields))
		}
	}
	if loose := looseParts(parts); len(loose) > 0 {
		should := make([]query.Query, 0, len(loose))
		for _, part := range loose {
			should = append(should, fieldQuery(part, mode == relaxAny, fields))
		}
		dq := bleve.NewDisjunctionQuery(should...)
		if mode == relaxAny {
			dq.SetMin(1)
		} else {
			dq.SetMin(float64(minShouldMatch(len(should))))
		}
		must = append(must, dq)
	}
	if len(must) == 1 {
		return must[0]
	}
	return bleve.NewConjunctionQuery(must...)
}

// searchFields resolves a Query's field restriction to the boost table rows it
// names, keeping the table's own order and boosts. An empty restriction is the
// whole table.
//
// A name the table does not carry is dropped rather than ignored, so a caller
// that asks only for unknown fields gets a query that matches nothing. That is
// the honest answer: the alternative -- quietly widening back to every field --
// would turn a title-only filter into an archive-wide search and report the
// result as if the filter had been applied.
func searchFields(names []string) []boostedField {
	if len(names) == 0 {
		return boostedTextFields
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		wanted[name] = struct{}{}
	}
	fields := make([]boostedField, 0, len(names))
	for _, f := range boostedTextFields {
		if _, ok := wanted[f.field]; ok {
			fields = append(fields, f)
		}
	}
	return fields
}

// fieldQuery matches one query part across every searchable field, at that
// field's boost. With fuzzy on, a long word also matches with one edit of slack
// at half the boost, so an exact match always outranks an approximate one
// rather than merely appearing beside it.
//
// Known limitation: a part that analyzes to no tokens at all (a lone "—" or
// "#") becomes a match-none clause that still counts toward the disjunction's
// numerator, tightening a relaxSome floor it can never satisfy. The relaxAny
// rung absorbs it, and detecting it properly needs the index's analyzer.
func fieldQuery(part queryPart, fuzzy bool, fields []boostedField) query.Query {
	disjuncts := make([]query.Query, 0, 2*len(fields))
	for _, f := range fields {
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
	if len(disjuncts) == 1 {
		return disjuncts[0]
	}
	return bleve.NewDisjunctionQuery(disjuncts...)
}

// fuzzyWorthy decides which terms get an edit of slack. Short words are out
// because one edit reaches most of the dictionary from them, and anything with
// a digit is out because it is an id, an amount or a date — the values where a
// near miss is a different document, not the same one spelled badly.
//
// Fuzziness is confined to the relaxAny rung on cost grounds: it adds a
// dictionary automaton scan per field per term, over an OCR vocabulary full of
// garbage tokens, so only a query that already found nothing pays for it.
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

func anyFuzzyWorthy(parts []queryPart) bool {
	for _, part := range parts {
		if fuzzyWorthy(part.text) {
			return true
		}
	}
	return false
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
	flush := func(phrase, closed bool) {
		s := strings.TrimSpace(buf.String())
		buf.Reset()
		if s != "" {
			parts = append(parts, queryPart{text: s, phrase: phrase, closed: closed})
		}
	}
	for _, r := range q {
		switch {
		case r == '"':
			if inQuote {
				flush(true, true)
				inQuote = false
			} else {
				flush(false, false)
				inQuote = true
			}
		case unicode.IsSpace(r) && !inQuote:
			flush(false, false)
		default:
			buf.WriteRune(r)
		}
	}
	flush(inQuote, false)
	return parts
}

func plainFragment(s string) string {
	s = strings.ReplaceAll(s, "<mark>", "")
	s = strings.ReplaceAll(s, "</mark>", "")
	s = html.UnescapeString(s)
	return strings.TrimSpace(s)
}
