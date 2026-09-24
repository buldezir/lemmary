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

	"lemmary/backend/internal/models"
)

const (
	defaultSearchLimit = 10
	// MaxSearchLimit is the largest page size Search will return. Callers must
	// use this cap when computing offsets so pages do not skip hits.
	MaxSearchLimit = 500
	// The last rung's hits carry one keyword out of several, so a long list of
	// them is noise; the coord factor has already put the best coverage first.
	relaxedFallbackLimit = 10
	// Under this length a prefix reaches most of the vocabulary. See prefixTerm.
	minPrefixLen = 3
	// Low enough that the widest prefix match (title, boost 4) still scores
	// under the narrowest whole-word match (ocr_text, boost 1).
	prefixBoost = 0.2
)

// Text that is not empty but tokenises to nothing (a lone quote, punctuation).
// A sentinel so a caller can answer with an empty page rather than a 500.
var ErrNoSearchableTerms = errors.New("query has no searchable terms")

const (
	OwnerMine   = "mine"
	OwnerShared = "shared"
)

type Query struct {
	Text             string
	UserID           string
	ProcessingStatus string
	DocumentTypeIDs  []string
	CorrespondentIDs []string
	// TagIDs keeps documents carrying any of them; AllTagIDs, every one.
	TagIDs    []string
	AllTagIDs []string
	// Untagged keeps only documents with no tags; with a tag id it is nothing.
	Untagged bool
	DateFrom  string
	DateTo    string
	// Undated keeps only documents with no document_date. Asking for both this
	// and a date range is asking for nothing, which the conjunction answers.
	Undated bool
	// Owner narrows by who owns a readable document: OwnerMine, OwnerShared
	// (another account shared it with the caller), or empty for both.
	Owner string
	// Fields narrows the text match to named index fields; empty means every
	// field. The paperless-ngx layer uses it for title-only/content-only.
	Fields []string
	Offset int
	Limit  int
	// Relaxed drops the requirement that every unquoted term match. Off for the
	// Documents page, where the box is a filter and a two-of-three match reads
	// as a bug; on for the agent's tools, where the query is a guess.
	Relaxed bool
}

type Hit struct {
	ID         string
	Score      float64
	OCRSnippet string
	// Every OCR highlight fragment, best first; OCRSnippet is the first. The
	// agent quotes several: one fragment of ten pages is a hint, not an answer.
	OCRFragments []string
}

type Result struct {
	Hits  []Hit
	Total uint64
	// Required < Terms means every hit is a partial match, and Total is then
	// "documents matching at least Required terms", not comparable with the
	// strict path's Total. Both zero when Relaxed is off.
	Terms    int
	Required int
}

type queryPart struct {
	text   string
	phrase bool
	// closed is false for a phrase that never got its closing quote; a relaxed
	// query demotes it to a loose term, since that is a typo not an instruction.
	closed bool
}

type relaxMode int

const (
	relaxOff  relaxMode = iota // every term mandatory (Documents page, ngxapi)
	relaxSome                  // minShouldMatch(n) of n terms
	relaxAny                   // 1 of n terms, plus a fuzzy leg per long term
)

// searchPlan is the relaxation ladder for one query.
type searchPlan struct {
	primary  query.Query
	fallback query.Query // nil unless widening further would change the query
	terms    int
	required int
	// What Required becomes once the wider rung answers. Unchanged for a strict
	// query: that rung widens terms into prefixes, it does not drop any.
	fallbackRequired int
}

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
			required = plan.fallbackRequired
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

// EligibleIDs lists the documents satisfying everything in q except its text.
// complete is false when there were more than limit, the caller's signal that
// the list cannot be used as a pre-filter for the chunk index.
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
		// One over the limit, so a full page is distinguishable from one that
		// happened to end exactly there.
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

// MatchingIDs enumerates documents matching q strictly, up to limit, without
// highlighting. total is the full match count whatever the limit; complete is
// whether ids holds all of it. Search is the wrong tool here: it highlights.
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
		bq, err := chosenRung(b, plan)
		if err != nil {
			return err
		}
		offset := 0
		for len(ids) < limit {
			page := min(MaxSearchLimit, limit-len(ids))
			req := bleve.NewSearchRequestOptions(bq, page, offset, false)
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

// The text is required; a filters-only count is the database's job.
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
		res, err := countRung(b, plan.primary)
		if err != nil {
			return err
		}
		if res.Total == 0 && plan.fallback != nil {
			res, err = countRung(b, plan.fallback)
			if err != nil {
				return err
			}
		}
		total = res.Total
		return nil
	})
	return total, err
}

// Counting and listing have to agree with the list the user is looking at, so
// the escalation ladder belongs to the query rather than to Search.
func chosenRung(b bleve.Index, plan searchPlan) (query.Query, error) {
	if plan.fallback == nil {
		return plan.primary, nil
	}
	res, err := countRung(b, plan.primary)
	if err != nil {
		return nil, err
	}
	if res.Total > 0 {
		return plan.primary, nil
	}
	return plan.fallback, nil
}

func countRung(b bleve.Index, bq query.Query) (*bleve.SearchResult, error) {
	res, err := b.Search(bleve.NewSearchRequestOptions(bq, 0, 0, false))
	if err != nil {
		return nil, fmt.Errorf("bleve search: %w", err)
	}
	return res, nil
}

// The post-filter for the case EligibleIDs could not pre-filter: the dense list
// is short, so asking about its documents beats enumerating every allowed one.
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
		plan := searchPlan{primary: withFilters(textQuery(parts, relaxOff, false, fields), filters)}
		if anyPrefixWorthy(parts) {
			plan.fallback = withFilters(textQuery(parts, relaxOff, true, fields), filters)
		}
		return plan, nil
	}

	loose := looseParts(parts)
	plan := searchPlan{
		primary:          withFilters(textQuery(parts, relaxSome, false, fields), filters),
		terms:            len(loose),
		fallbackRequired: 1,
	}
	if len(loose) == 0 {
		// Every part is a closed phrase, so there is nothing left to relax.
		return plan, nil
	}
	plan.required = minShouldMatch(len(loose))
	// Only build the wider rung when it would actually differ: a single short
	// term is already its own floor.
	if plan.required > 1 || anyFuzzyWorthy(loose) || anyPrefixWorthy(loose) {
		plan.fallback = withFilters(textQuery(parts, relaxAny, true, fields), filters)
	}
	return plan, nil
}

// Everything in a Query except its text. Mandatory in every relax mode.
func filterConjuncts(q Query) []query.Query {
	conjuncts := make([]query.Query, 0, 6)
	if userID := strings.TrimSpace(q.UserID); userID != "" {
		switch q.Owner {
		case OwnerMine:
			conjuncts = append(conjuncts, termQuery(FieldOwner, userID))
		case OwnerShared:
			// Readable by me and owned by somebody else. Expressed as one
			// boolean because a lone MustNot matches nothing in bleve.
			shared := bleve.NewBooleanQuery()
			shared.AddMust(termQuery(FieldUser, userID))
			shared.AddMustNot(termQuery(FieldOwner, userID))
			conjuncts = append(conjuncts, shared)
		default:
			conjuncts = append(conjuncts, termQuery(FieldUser, userID))
		}
	}
	if status := strings.TrimSpace(q.ProcessingStatus); status != "" && status != "all" {
		if status == models.StatusFilterUnfinished {
			conjuncts = append(conjuncts, anyTermQuery(FieldProcessingStatus, models.UnfinishedDocStatuses))
		} else {
			conjuncts = append(conjuncts, termQuery(FieldProcessingStatus, status))
		}
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
	conjuncts = append(conjuncts, termQueries(FieldTags, q.AllTagIDs)...)
	if q.Untagged {
		conjuncts = append(conjuncts, untaggedQuery())
	}
	if dateQuery := dateRangeQuery(q.DateFrom, q.DateTo); dateQuery != nil {
		conjuncts = append(conjuncts, dateQuery)
	}
	if q.Undated {
		conjuncts = append(conjuncts, undatedQuery())
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

// Whether q restricts by anything but its owner, i.e. by filters the chunk
// index cannot apply itself.
func HasDocumentFilters(q Query) bool {
	bare := q
	bare.UserID = ""
	return len(filterConjuncts(bare)) > 0
}

// What a relaxed query is allowed to drop: everything but a closed phrase.
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

// A closed quoted phrase is an instruction, not a guess, so it stays mandatory
// however many loose terms surround it.
func mandatoryPhrase(part queryPart) bool {
	return part.phrase && part.closed
}

func textQuery(parts []queryPart, mode relaxMode, prefix bool, fields []boostedField) query.Query {
	if mode == relaxOff {
		conjuncts := make([]query.Query, 0, len(parts))
		for _, part := range parts {
			conjuncts = append(conjuncts, fieldQuery(part, false, prefix, fields))
		}
		if len(conjuncts) == 1 {
			return conjuncts[0]
		}
		return bleve.NewConjunctionQuery(conjuncts...)
	}

	must := make([]query.Query, 0, len(parts))
	for _, part := range parts {
		if mandatoryPhrase(part) {
			must = append(must, fieldQuery(part, false, prefix, fields))
		}
	}
	if loose := looseParts(parts); len(loose) > 0 {
		should := make([]query.Query, 0, len(loose))
		for _, part := range loose {
			should = append(should, fieldQuery(part, mode == relaxAny, prefix, fields))
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

// An unknown field name is dropped, so asking only for unknown fields matches
// nothing: widening back to every field would turn a title-only filter into an
// archive-wide search and report it as if the filter had applied.
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

// With fuzzy on, a long word also matches with one edit of slack at half the
// boost, so an exact match outranks an approximate one.
//
// Known limitation: a part analyzing to no tokens (a lone "—") becomes a
// match-none clause that still counts toward the disjunction's numerator,
// tightening a relaxSome floor it can never satisfy. relaxAny absorbs it;
// detecting it properly needs the index's analyzer.
func fieldQuery(part queryPart, fuzzy, prefix bool, fields []boostedField) query.Query {
	prefixText := ""
	if prefix {
		prefixText = prefixTerm(part.text)
	}
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

		if prefixText != "" {
			pq := bleve.NewPrefixQuery(prefixText)
			pq.SetField(f.field)
			pq.SetBoost(f.boost * prefixBoost)
			disjuncts = append(disjuncts, pq)
		}

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

// prefixTerm is the dictionary prefix a word should also match, or "" for a
// word that must not be used as one. A match query compares whole terms, so a
// half-typed "amaz" would otherwise miss "Amazon".
//
// Prefix legs only ever appear on a widening rung: a prefix query walks the
// field dictionary and scores every term it finds, measured at 415ms for one
// three-letter term over an OCR-sized vocabulary. Running it only after the
// strict query found nothing also makes "exact beats prefix" structural.
//
// ponytail: an empty-result query still pays one wide expansion. Bound it with
// a dictionary-size check (FieldDictPrefix) if a large archive makes typing
// through a dense prefix slow.
//
// A prefix query is not analyzed, so the analyzer is approximated here: lower
// case, leading token only, or a trailing comma would be searched for
// literally. Digits are out for the reason fuzzyWorthy gives.
//
// ponytail: prefix, not substring -- "mazon" still misses "Amazon". Needs an
// ngram field and a mapping version bump to reindex behind it.
func prefixTerm(term string) string {
	term = strings.ToLower(strings.TrimSpace(term))
	term = strings.TrimFunc(term, isSeparator)
	if i := strings.IndexFunc(term, isSeparator); i >= 0 {
		term = term[:i]
	}
	if utf8.RuneCountInString(term) < minPrefixLen || hasDigit(term) {
		return ""
	}
	return term
}

func anyPrefixWorthy(parts []queryPart) bool {
	for _, part := range parts {
		if !mandatoryPhrase(part) && prefixTerm(part.text) != "" {
			return true
		}
	}
	return false
}

// What the unicode tokenizer splits on, near enough.
func isSeparator(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// Short words are out because one edit reaches most of the dictionary from
// them; anything with a digit is out because a near miss on an id, amount or
// date is a different document. Confined to the relaxAny rung on cost grounds:
// one dictionary automaton scan per field per term over an OCR vocabulary.
func fuzzyWorthy(term string) bool {
	return utf8.RuneCountInString(term) >= 5 && !hasDigit(term)
}

func hasDigit(term string) bool {
	return strings.ContainsFunc(term, unicode.IsDigit)
}

func anyFuzzyWorthy(parts []queryPart) bool {
	for _, part := range parts {
		if fuzzyWorthy(part.text) {
			return true
		}
	}
	return false
}

// How many of n loose terms a document must carry. Two terms both have to
// match, since dropping one leaves a single word that matches the archive.
// From three up one may be missing; past five a fraction takes over.
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

func termQueries(field string, values []string) []query.Query {
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
	return queries
}

func anyTermQuery(field string, values []string) query.Query {
	queries := termQueries(field, values)
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

// bleve has no field-exists query and refuses a range open at both ends, so
// "no date" is the negation of its own RFC3339 bounds, the widest range it can
// express.
func undatedQuery() query.Query {
	dq := bleve.NewDateRangeQuery(query.MinRFC3339CompatibleTime, query.MaxRFC3339CompatibleTime)
	dq.SetField(FieldDocumentDate)
	bq := bleve.NewBooleanQuery()
	bq.AddMustNot(dq)
	return bq
}

func untaggedQuery() query.Query {
	anyTag := bleve.NewWildcardQuery("*")
	anyTag.SetField(FieldTags)
	bq := bleve.NewBooleanQuery()
	bq.AddMustNot(anyTag)
	return bq
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
