package ngxapi

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/fulltext"
)

// truncatedContentLen is how much of the OCR text a list response carries when
// the client asks for truncated content, matching paperless-ngx's own cut.
const truncatedContentLen = 550

// textCriterion is one text filter and the index fields it may match. Empty
// fields means every searchable field, which is what a bare `query=` asks for;
// paperless' title- and content-scoped filters name the fields they mean.
type textCriterion struct {
	text   string
	fields []string
}

// documentFilters is a parsed paperless-ngx document list query. Ids are
// already translated to PocketBase ids, and dates are already normalised to an
// inclusive YYYY-MM-DD range, so building the SQL from it needs no further
// knowledge of paperless' parameter spelling.
type documentFilters struct {
	text []textCriterion

	tagsAll  []string
	tagsAny  []string
	tagsNone []string
	isTagged *bool

	docTypes     []string
	docTypesNone []string
	docTypeUnset *bool

	corrs     []string
	corrsNone []string
	corrUnset *bool

	// createdFrom/To filter document_date, addedFrom/To the created timestamp.
	createdFrom, createdTo string
	addedFrom, addedTo     string

	ids []string

	truncateContent bool

	// impossible means the filter set cannot match anything: a positive filter
	// named an id that does not exist for this owner, or asked for a property
	// no document here can have. The result is an empty page -- the client
	// asked for documents carrying a tag nobody has, and the honest answer is
	// none of them, never the unfiltered archive that silently dropping the
	// filter would return.
	impossible bool
}

// hasText reports whether the query needs the search index at all. A pure tag
// or date filter must keep working while the index is still building.
func (f documentFilters) hasText() bool {
	return len(f.text) > 0
}

// handledParams are the query parameters the document list understands. The
// value says how the parameter is read, which is also the switch the parser
// runs on.
//
// Anything not in here is refused rather than ignored. Ignoring an unknown
// filter is the bug this whole file exists to fix: the client renders a 200 as
// though the filter had been applied, so "documents tagged Invoice" quietly
// becomes "every document". A 400 is wrong far more visibly.
var handledParams = map[string]struct{}{
	"query": {}, "title_content": {}, "title__icontains": {}, "content__icontains": {},

	"tags__id": {}, "tags__id__all": {}, "tags__id__in": {}, "tags__id__none": {},
	"is_tagged": {},

	"document_type__id": {}, "document_type__id__in": {},
	"document_type__id__none": {}, "document_type__isnull": {},

	"correspondent__id": {}, "correspondent__id__in": {},
	"correspondent__id__none": {}, "correspondent__isnull": {},

	"created__date__gt": {}, "created__date__gte": {},
	"created__date__lt": {}, "created__date__lte": {},
	"created__gt": {}, "created__gte": {}, "created__lt": {}, "created__lte": {},
	"created__year": {},

	"added__date__gt": {}, "added__date__gte": {},
	"added__date__lt": {}, "added__date__lte": {},
	"added__gt": {}, "added__gte": {}, "added__lt": {}, "added__lte": {},

	"id": {}, "id__in": {},

	"storage_path__isnull": {},

	// Not filters: paging, ordering, and response shaping.
	"page": {}, "page_size": {}, "ordering": {},
	"format": {}, "full_perms": {}, "truncate_content": {},
}

// parseDocumentFilters reads a paperless-ngx document list query. The returned
// error is already client-facing text.
func parseDocumentFilters(app core.App, authID string, q url.Values) (documentFilters, error) {
	return parseDocumentFiltersWith(&ngxIDs{app: app, authID: authID, byCollection: map[string]map[int]string{}}, q)
}

// parseDocumentFiltersWith is parseDocumentFilters with the id resolver handed
// in, which is the seam the parser tests use: everything except id translation
// is pure, and pre-seeding the resolver keeps it that way.
func parseDocumentFiltersWith(ids *ngxIDs, q url.Values) (documentFilters, error) {
	var f documentFilters

	for name := range q {
		if _, ok := handledParams[name]; !ok {
			return f, fmt.Errorf("Unsupported filter %q.", name)
		}
	}

	f.text = parseTextCriteria(q)

	// Positive id filters: an id that does not resolve makes the whole query
	// impossible. Negative ones just have nothing to exclude, so they drop it.
	for _, name := range []string{"tags__id", "tags__id__all"} {
		resolved, ok, err := ids.resolveAll("tags", csvValues(q, name))
		if err != nil {
			return f, err
		}
		f.impossible = f.impossible || !ok
		f.tagsAll = append(f.tagsAll, resolved...)
	}
	if raw := csvValues(q, "tags__id__in"); len(raw) > 0 {
		resolved, _, err := ids.resolveAll("tags", raw)
		if err != nil {
			return f, err
		}
		// An "any of" list that resolved to nothing can match nothing.
		f.impossible = f.impossible || len(resolved) == 0
		f.tagsAny = resolved
	}
	if raw := csvValues(q, "tags__id__none"); len(raw) > 0 {
		resolved, _, err := ids.resolveAll("tags", raw)
		if err != nil {
			return f, err
		}
		f.tagsNone = resolved
	}

	for _, spec := range []struct {
		collection       string
		single, in, none string
		isnull           string
		dst, dstNone     *[]string
		unset            **bool
	}{
		{"document_types", "document_type__id", "document_type__id__in", "document_type__id__none", "document_type__isnull", &f.docTypes, &f.docTypesNone, &f.docTypeUnset},
		{"correspondents", "correspondent__id", "correspondent__id__in", "correspondent__id__none", "correspondent__isnull", &f.corrs, &f.corrsNone, &f.corrUnset},
	} {
		raw := append(csvValues(q, spec.single), csvValues(q, spec.in)...)
		if len(raw) > 0 {
			resolved, _, err := ids.resolveAll(spec.collection, raw)
			if err != nil {
				return f, err
			}
			f.impossible = f.impossible || len(resolved) == 0
			*spec.dst = resolved
		}
		if raw := csvValues(q, spec.none); len(raw) > 0 {
			resolved, _, err := ids.resolveAll(spec.collection, raw)
			if err != nil {
				return f, err
			}
			*spec.dstNone = resolved
		}
		v, err := boolParam(q, spec.isnull)
		if err != nil {
			return f, err
		}
		*spec.unset = v
	}

	if raw := csvValues(q, "id"); len(raw) > 0 {
		resolved, _, err := ids.resolveAll("documents", raw)
		if err != nil {
			return f, err
		}
		f.impossible = f.impossible || len(resolved) == 0
		f.ids = resolved
	}
	if raw := csvValues(q, "id__in"); len(raw) > 0 {
		resolved, _, err := ids.resolveAll("documents", raw)
		if err != nil {
			return f, err
		}
		f.impossible = f.impossible || len(resolved) == 0
		f.ids = append(f.ids, resolved...)
	}

	tagged, err := boolParam(q, "is_tagged")
	if err != nil {
		return f, err
	}
	f.isTagged = tagged

	// Lemmary has no storage paths, so every document is "without one".
	storageUnset, err := boolParam(q, "storage_path__isnull")
	if err != nil {
		return f, err
	}
	if storageUnset != nil && !*storageUnset {
		f.impossible = true
	}

	if f.createdFrom, f.createdTo, err = parseDateRange(q, "created"); err != nil {
		return f, err
	}
	if f.addedFrom, f.addedTo, err = parseDateRange(q, "added"); err != nil {
		return f, err
	}

	truncate, err := boolParam(q, "truncate_content")
	if err != nil {
		return f, err
	}
	f.truncateContent = truncate != nil && *truncate

	return f, nil
}

// parseTextCriteria collects the text filters in a fixed order: the general
// query first, because when several are present its ranking is the one worth
// keeping.
func parseTextCriteria(q url.Values) []textCriterion {
	var out []textCriterion
	for _, spec := range []struct {
		param  string
		fields []string
	}{
		{"query", nil},
		{"title_content", []string{fulltext.FieldTitle, fulltext.FieldTitleOriginal, fulltext.FieldOCRText}},
		{"title__icontains", []string{fulltext.FieldTitle, fulltext.FieldTitleOriginal}},
		{"content__icontains", []string{fulltext.FieldOCRText}},
	} {
		if text := strings.TrimSpace(q.Get(spec.param)); text != "" {
			out = append(out, textCriterion{text: text, fields: spec.fields})
		}
	}
	return out
}

// ngxIDs translates the FNV-hashed ids clients see back into PocketBase ids for
// the life of one request.
//
// The hash is one-way, so the only way back is to hash every candidate. Holding
// the map per collection is what keeps a request naming three tag ids to one
// pass over the tag table instead of three.
type ngxIDs struct {
	app          core.App
	authID       string
	byCollection map[string]map[int]string
}

func (r *ngxIDs) load(collection string) (map[int]string, error) {
	if known, ok := r.byCollection[collection]; ok {
		return known, nil
	}
	// Deliberately a fresh scan rather than the shared cache: a filter reads a
	// miss as "no such tag, match nothing", so serving it a map built before
	// the tag existed would answer an empty page for a document the client can
	// see. The result still warms the cache for the item lookups that follow --
	// listing a page of documents is immediately followed by fetching their
	// thumbnails.
	known, err := ngxIDMap(r.app, collection, r.authID)
	if err != nil {
		return nil, err
	}
	ngxIDScopes.put(ngxIDScope{collection: collection, owner: r.authID}, known)
	r.byCollection[collection] = known
	return known, nil
}

// resolveAll maps client ids to PocketBase ids. complete is false when any of
// them named a record that does not exist, which is what tells a positive
// filter to match nothing rather than everything.
func (r *ngxIDs) resolveAll(collection string, raw []string) (resolved []string, complete bool, err error) {
	if len(raw) == 0 {
		return nil, true, nil
	}
	known, err := r.load(collection)
	if err != nil {
		return nil, false, err
	}

	complete = true
	seen := map[string]struct{}{}
	for _, value := range raw {
		ngxID, convErr := strconv.Atoi(value)
		if convErr != nil {
			return nil, false, fmt.Errorf("Invalid id %q.", value)
		}
		pbID, ok := known[ngxID]
		if !ok {
			complete = false
			continue
		}
		if _, dup := seen[pbID]; dup {
			continue
		}
		seen[pbID] = struct{}{}
		resolved = append(resolved, pbID)
	}
	return resolved, complete, nil
}

// csvValues reads a parameter that may be repeated, comma-separated, or both.
func csvValues(q url.Values, name string) []string {
	var out []string
	for _, raw := range q[name] {
		for _, part := range strings.Split(raw, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// boolParam reads a Django-style boolean. Absent is nil, which is distinct from
// false: document_type__isnull=false is a filter, and its absence is not.
func boolParam(q url.Values, name string) (*bool, error) {
	raw := strings.TrimSpace(q.Get(name))
	if raw == "" {
		return nil, nil
	}
	switch strings.ToLower(raw) {
	case "true", "1":
		v := true
		return &v, nil
	case "false", "0":
		v := false
		return &v, nil
	default:
		return nil, fmt.Errorf("Invalid boolean value %q for %q.", raw, name)
	}
}

// parseDateRange folds every comparator paperless spells for one field into a
// single inclusive [from, to] pair of YYYY-MM-DD days. Strict bounds become the
// neighbouring day, and repeated bounds keep the tighter one, so
// created__date__gt=2025-01-01&created__date__gte=2025-06-01 means June.
func parseDateRange(q url.Values, field string) (from, to string, err error) {
	for _, spec := range []struct {
		suffix string
		lower  bool
		shift  int
	}{
		{"__date__gt", true, 1},
		{"__gt", true, 1},
		{"__date__gte", true, 0},
		{"__gte", true, 0},
		{"__date__lt", false, -1},
		{"__lt", false, -1},
		{"__date__lte", false, 0},
		{"__lte", false, 0},
	} {
		raw := strings.TrimSpace(q.Get(field + spec.suffix))
		if raw == "" {
			continue
		}
		day, dayErr := parseFilterDay(raw)
		if dayErr != nil {
			return "", "", fmt.Errorf("Invalid date %q for %q.", raw, field+spec.suffix)
		}
		bound := day.AddDate(0, 0, spec.shift).Format("2006-01-02")
		if spec.lower {
			if from == "" || bound > from {
				from = bound
			}
			continue
		}
		if to == "" || bound < to {
			to = bound
		}
	}

	if raw := strings.TrimSpace(q.Get(field + "__year")); raw != "" {
		year, convErr := strconv.Atoi(raw)
		if convErr != nil || year < 1 || year > 9999 {
			return "", "", fmt.Errorf("Invalid year %q for %q.", raw, field+"__year")
		}
		lower := fmt.Sprintf("%04d-01-01", year)
		upper := fmt.Sprintf("%04d-12-31", year)
		if from == "" || lower > from {
			from = lower
		}
		if to == "" || upper < to {
			to = upper
		}
	}

	return from, to, nil
}

// parseFilterDay accepts the day itself or a full timestamp, which is what
// clients send interchangeably for the same filter.
func parseFilterDay(raw string) (time.Time, error) {
	if len(raw) > 10 {
		raw = raw[:10]
	}
	return time.Parse("2006-01-02", raw)
}

// documentFilterExprs compiles the filter set to SQL.
//
// Raw expressions rather than PocketBase's filter DSL, because the same slice
// has to serve CountRecords and the page query: the DSL only reaches the latter,
// and a count built from a different expression than the page it counts is a
// pagination bug waiting to happen.
//
// Column references are qualified because both queries read FROM documents
// unaliased, and because the json_each subquery correlates on the outer row.
func documentFilterExprs(f documentFilters) []dbx.Expression {
	var exprs []dbx.Expression
	names := &paramNamer{}

	if f.impossible {
		return []dbx.Expression{dbx.NewExp("1 = 0")}
	}

	// Every tag on its own conjunct: "all of these" is many "has this", where
	// one IN list would only ever mean "any of these".
	for _, id := range f.tagsAll {
		exprs = append(exprs, tagsExpr([]string{id}, false, names))
	}
	if len(f.tagsAny) > 0 {
		exprs = append(exprs, tagsExpr(f.tagsAny, false, names))
	}
	if len(f.tagsNone) > 0 {
		exprs = append(exprs, tagsExpr(f.tagsNone, true, names))
	}
	if f.isTagged != nil {
		if *f.isTagged {
			exprs = append(exprs, dbx.NewExp(hasAnyTagSQL))
		} else {
			exprs = append(exprs, dbx.NewExp("json_array_length("+tagsJSON+") = 0"))
		}
	}

	for _, spec := range []struct {
		column string
		in     []string
		none   []string
		unset  *bool
	}{
		{"document_type", f.docTypes, f.docTypesNone, f.docTypeUnset},
		{"correspondent", f.corrs, f.corrsNone, f.corrUnset},
	} {
		if len(spec.in) > 0 {
			exprs = append(exprs, dbx.In("documents."+spec.column, anyValues(spec.in)...))
		}
		if len(spec.none) > 0 {
			// COALESCE, because a document with no relation at all must survive
			// an exclusion: SQL three-valued logic drops NULL from a bare
			// NOT IN, where paperless' exclude() keeps it. Measured on a table
			// with a NULL and an empty document_type, the bare form loses both.
			placeholders, params := names.bind(spec.column+"_none", spec.none)
			exprs = append(exprs, dbx.NewExp(fmt.Sprintf(
				"COALESCE([[documents.%s]], '') NOT IN (%s)",
				spec.column, strings.Join(placeholders, ", "),
			), params))
		}
		if spec.unset != nil {
			op := "="
			if !*spec.unset {
				op = "!="
			}
			exprs = append(exprs, dbx.NewExp(
				fmt.Sprintf("COALESCE([[documents.%s]], '') %s ''", spec.column, op),
			))
		}
	}

	exprs = append(exprs, dateRangeExprs("document_date", f.createdFrom, f.createdTo, names)...)
	exprs = append(exprs, dateRangeExprs("created", f.addedFrom, f.addedTo, names)...)

	if len(f.ids) > 0 {
		exprs = append(exprs, dbx.In("documents.id", anyValues(f.ids)...))
	}

	return exprs
}

// tagsJSON normalises the tags column to a JSON array before anything reads it.
//
// The column is a multi-relation, which PocketBase stores as a JSON array in a
// TEXT column, but it also holds two non-arrays: the legacy empty string, and
// NULL. Testing `json_valid(tags) AND ...` looks equivalent and is not --
// json_valid(NULL) is NULL, so under negation the whole conjunct goes NULL and
// the row is dropped. Measured: `NOT (json_valid(tags) AND json_array_length(
// tags) > 0)` returns two of the three untagged rows, losing the NULL one. The
// CASE returns all three.
const tagsJSON = `CASE WHEN json_valid([[documents.tags]]) THEN [[documents.tags]] ELSE '[]' END`

// hasAnyTagSQL is the "this document carries at least one tag" test.
const hasAnyTagSQL = `json_array_length(` + tagsJSON + `) > 0`

// tagsExpr tests membership against the tag id array.
func tagsExpr(ids []string, negate bool, names *paramNamer) dbx.Expression {
	placeholders, params := names.bind("tag", ids)
	exists := "EXISTS"
	if negate {
		exists = "NOT EXISTS"
	}
	return dbx.NewExp(fmt.Sprintf(
		"%s (SELECT 1 FROM json_each(%s) t WHERE t.value IN (%s))",
		exists, tagsJSON, strings.Join(placeholders, ", "),
	), params)
}

// dateRangeExprs compares the first ten characters of a date column: the column
// is TEXT and holds both "YYYY-MM-DD" and "YYYY-MM-DD HH:MM:SS.sssZ". An empty
// date is excluded from any range rather than sorting below every bound.
func dateRangeExprs(column, from, to string, names *paramNamer) []dbx.Expression {
	if from == "" && to == "" {
		return nil
	}
	exprs := []dbx.Expression{
		dbx.NewExp(fmt.Sprintf("COALESCE([[documents.%s]], '') != ''", column)),
	}
	if from != "" {
		placeholder, params := names.bindOne(column+"_from", from)
		exprs = append(exprs, dbx.NewExp(
			fmt.Sprintf("substr([[documents.%s]], 1, 10) >= %s", column, placeholder), params,
		))
	}
	if to != "" {
		placeholder, params := names.bindOne(column+"_to", to)
		exprs = append(exprs, dbx.NewExp(
			fmt.Sprintf("substr([[documents.%s]], 1, 10) <= %s", column, placeholder), params,
		))
	}
	return exprs
}

// paramNamer hands out placeholder names that are unique across one query. dbx
// merges every expression's parameters into one map, so two expressions reusing
// a name would silently overwrite each other's value.
type paramNamer struct {
	n int
}

func (p *paramNamer) bindOne(prefix, value string) (placeholder string, params dbx.Params) {
	names, params := p.bind(prefix, []string{value})
	return names[0], params
}

func (p *paramNamer) bind(prefix string, values []string) (placeholders []string, params dbx.Params) {
	params = dbx.Params{}
	placeholders = make([]string, 0, len(values))
	for _, value := range values {
		name := fmt.Sprintf("ngxf_%s_%d", prefix, p.n)
		p.n++
		params[name] = value
		placeholders = append(placeholders, "{:"+name+"}")
	}
	return placeholders, params
}

func anyValues(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, v)
	}
	return out
}
