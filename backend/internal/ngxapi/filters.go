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

// dateBound is one comparison against a date column. value is already in the
// spelling the column is compared against; day compares the leading ten
// characters only, which is all a date filter can mean.
type dateBound struct {
	op    string // ">=", ">", "<=" or "<"
	value string
	day   bool
}

// documentFilters is a parsed paperless-ngx document list query. Ids are
// already translated to PocketBase ids, and dates are already normalised to
// bounds the SQL can compare directly, so building the query from it needs no
// further knowledge of paperless' parameter spelling.
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

	// created bounds the date a client is shown for a document; added bounds
	// the moment it was uploaded.
	created []dateBound
	added   []dateBound

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

// textParams are the text filters and the index fields each may match. The
// general query is first because its ranking is the one worth keeping.
var textParams = []struct {
	param  string
	fields []string
}{
	{"query", nil},
	{"title_content", []string{fulltext.FieldTitle, fulltext.FieldTitleOriginal, fulltext.FieldOCRText}},
	{"title__icontains", []string{fulltext.FieldTitle, fulltext.FieldTitleOriginal}},
	{"content__icontains", []string{fulltext.FieldOCRText}},
}

// relationSpec is one single-relation filter family.
type relationSpec struct {
	collection               string
	single, in, none, isnull string
}

var relationSpecs = []relationSpec{
	{
		collection: "document_types",
		single:     "document_type__id", in: "document_type__id__in",
		none: "document_type__id__none", isnull: "document_type__isnull",
	},
	{
		collection: "correspondents",
		single:     "correspondent__id", in: "correspondent__id__in",
		none: "correspondent__id__none", isnull: "correspondent__isnull",
	},
}

var tagParams = []string{"tags__id", "tags__id__all", "tags__id__in", "tags__id__none", "is_tagged"}

var documentIDParams = []string{"id", "id__in"}

// ownerParams: see applyOwnerFilters.
var ownerParams = []string{"owner__id", "owner__id__in", "owner__id__none", "owner__isnull"}

// otherParams are answered from what Lemmary does not have.
var otherParams = []string{"storage_path__isnull"}

// nonFilterParams shape the response rather than narrowing it.
var nonFilterParams = []string{
	"page", "page_size", "ordering",
	"format", "full_perms", "truncate_content",
	// swift-paperless sends `fields=id` on the sweep that reconciles remote
	// deletions and swallows the error, so a 400 here stops deletions reaching
	// the device. Lemmary always returns the full shape; this narrows nothing.
	"fields",
}

// dateFieldSpec is one paperless date filter family. datetime says the column
// carries a time of day, so the bare comparators compare the whole instant.
type dateFieldSpec struct {
	param    string
	datetime bool
}

var dateFieldSpecs = []dateFieldSpec{
	{param: "created", datetime: false},
	{param: "added", datetime: true},
}

// dateSuffixes are the comparators paperless spells. dayOnly marks the
// __date__ forms, which mean a day whatever the column holds.
var dateSuffixes = []struct {
	suffix  string
	op      string
	dayOnly bool
}{
	{"__date__gt", ">", true},
	{"__date__gte", ">=", true},
	{"__date__lt", "<", true},
	{"__date__lte", "<=", true},
	{"__gt", ">", false},
	{"__gte", ">=", false},
	{"__lt", "<", false},
	{"__lte", "<=", false},
}

// handledParams are the query parameters the document list understands.
//
// Anything not in here is refused rather than ignored. Ignoring an unknown
// filter is the bug this whole file exists to fix: the client renders a 200 as
// though the filter had been applied, so "documents tagged Invoice" quietly
// becomes "every document". A 400 is wrong far more visibly.
//
// Derived from the tables the parser reads rather than kept by hand: the two
// had already drifted, refusing added__year that the parser handled fine.
var handledParams = buildHandledParams()

func buildHandledParams() map[string]struct{} {
	out := map[string]struct{}{}
	add := func(names ...string) {
		for _, name := range names {
			out[name] = struct{}{}
		}
	}
	for _, spec := range textParams {
		add(spec.param)
	}
	for _, spec := range relationSpecs {
		add(spec.single, spec.in, spec.none, spec.isnull)
	}
	for _, field := range dateFieldSpecs {
		for _, suffix := range dateSuffixes {
			add(field.param + suffix.suffix)
		}
		add(field.param + "__year")
	}
	add(tagParams...)
	add(documentIDParams...)
	add(ownerParams...)
	add(otherParams...)
	add(nonFilterParams...)
	return out
}

// parseDocumentFilters reads a paperless-ngx document list query. The returned
// error is already client-facing text.
func parseDocumentFilters(app core.App, authID string, q url.Values) (documentFilters, error) {
	return parseDocumentFiltersWith(requestNgxIDs(app, authID), toNgxID(authID), q)
}

// parseDocumentFiltersWith is parseDocumentFilters with the id resolver and the
// caller's own client-facing id handed in, which is the seam the parser tests
// use: everything except id translation is pure.
func parseDocumentFiltersWith(ids *ngxIDs, ownerID int, q url.Values) (documentFilters, error) {
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

	// Destinations are paired with the specs here rather than stored in them,
	// so the parameter names stay in the one table handledParams derives from.
	for _, target := range []struct {
		spec         relationSpec
		dst, dstNone *[]string
		unset        **bool
	}{
		{relationSpecs[0], &f.docTypes, &f.docTypesNone, &f.docTypeUnset},
		{relationSpecs[1], &f.corrs, &f.corrsNone, &f.corrUnset},
	} {
		spec := target.spec
		raw := append(csvValues(q, spec.single), csvValues(q, spec.in)...)
		if len(raw) > 0 {
			resolved, _, err := ids.resolveAll(spec.collection, raw)
			if err != nil {
				return f, err
			}
			f.impossible = f.impossible || len(resolved) == 0
			*target.dst = resolved
		}
		if raw := csvValues(q, spec.none); len(raw) > 0 {
			resolved, _, err := ids.resolveAll(spec.collection, raw)
			if err != nil {
				return f, err
			}
			*target.dstNone = resolved
		}
		v, err := boolParam(q, spec.isnull)
		if err != nil {
			return f, err
		}
		*target.unset = v
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

	if err := applyOwnerFilters(&f, ownerID, q); err != nil {
		return f, err
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

	if f.created, err = parseDateBounds(q, dateFieldSpecs[0]); err != nil {
		return f, err
	}
	if f.added, err = parseDateBounds(q, dateFieldSpecs[1]); err != nil {
		return f, err
	}

	truncate, err := boolParam(q, "truncate_content")
	if err != nil {
		return f, err
	}
	f.truncateContent = truncate != nil && *truncate

	return f, nil
}

// applyOwnerFilters answers the owner pill from the one fact this endpoint
// guarantees: every document it can return belongs to the caller. So a filter
// either names them and narrows nothing, or names somebody else and matches
// nothing. Refusing these turned "My documents" into an error.
func applyOwnerFilters(f *documentFilters, ownerID int, q url.Values) error {
	named := func(param string) (map[int]bool, error) {
		raw := csvValues(q, param)
		if len(raw) == 0 {
			return nil, nil
		}
		out := make(map[int]bool, len(raw))
		for _, value := range raw {
			id, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("Invalid id %q.", value)
			}
			out[id] = true
		}
		return out, nil
	}

	for _, param := range []string{"owner__id", "owner__id__in"} {
		wanted, err := named(param)
		if err != nil {
			return err
		}
		if wanted != nil && !wanted[ownerID] {
			f.impossible = true
		}
	}

	excluded, err := named("owner__id__none")
	if err != nil {
		return err
	}
	if excluded[ownerID] {
		f.impossible = true
	}

	// Every document here has an owner.
	unowned, err := boolParam(q, "owner__isnull")
	if err != nil {
		return err
	}
	if unowned != nil && *unowned {
		f.impossible = true
	}
	return nil
}

// parseTextCriteria collects the text filters in textParams' order.
func parseTextCriteria(q url.Values) []textCriterion {
	var out []textCriterion
	for _, spec := range textParams {
		if text := strings.TrimSpace(q.Get(spec.param)); text != "" {
			out = append(out, textCriterion{text: text, fields: spec.fields})
		}
	}
	return out
}

// ngxIDs translates the integer ids a client sends into PocketBase ids for the
// life of one request.
//
// It resolves in batches and remembers what it resolved, so a request naming
// three tag ids costs one query rather than three, and naming the same tag in
// two filters costs nothing the second time.
type ngxIDs struct {
	// lookup is injected rather than called directly on an app: everything in
	// the parser except id translation is pure, and handing this in is what
	// lets the parser tests stay that way.
	lookup func(collection string, ids []int) (map[int]string, error)
	memo   map[string]map[int]string
}

func requestNgxIDs(app core.App, authID string) *ngxIDs {
	return &ngxIDs{
		lookup: func(collection string, ids []int) (map[int]string, error) {
			return pbIDsByNgxID(app, collection, authID, ids)
		},
		memo: map[string]map[int]string{},
	}
}

// resolve reads a batch of client ids, consulting the memo first and querying
// only for what is left.
func (r *ngxIDs) resolve(collection string, ids []int) (map[int]string, error) {
	known := r.memo[collection]
	if known == nil {
		known = map[int]string{}
		r.memo[collection] = known
	}

	pending := make([]int, 0, len(ids))
	for _, id := range ids {
		if _, ok := known[id]; !ok {
			pending = append(pending, id)
		}
	}
	if len(pending) == 0 || r.lookup == nil {
		return known, nil
	}

	found, err := r.lookup(collection, pending)
	if err != nil {
		return nil, err
	}
	for ngxID, pbID := range found {
		known[ngxID] = pbID
	}
	return known, nil
}

// resolveAll maps client ids to PocketBase ids. complete is false when any of
// them named a record that does not exist, which is what tells a positive
// filter to match nothing rather than everything.
func (r *ngxIDs) resolveAll(collection string, raw []string) (resolved []string, complete bool, err error) {
	if len(raw) == 0 {
		return nil, true, nil
	}

	wanted := make([]int, 0, len(raw))
	for _, value := range raw {
		ngxID, convErr := strconv.Atoi(value)
		if convErr != nil {
			return nil, false, fmt.Errorf("Invalid id %q.", value)
		}
		wanted = append(wanted, ngxID)
	}

	known, err := r.resolve(collection, wanted)
	if err != nil {
		return nil, false, err
	}

	complete = true
	seen := map[string]struct{}{}
	for _, ngxID := range wanted {
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

const (
	dayLayout = "2006-01-02"
	// storedTimeLayout is how PocketBase writes a datetime column. Bounds are
	// normalised to it so the comparison stays lexical over TEXT.
	storedTimeLayout = "2006-01-02 15:04:05.000Z"
)

// parseDateBounds reads every comparator paperless spells for one date field.
//
// One bound per comparator rather than a folded [from, to] pair: ANDing keeps
// the tightest automatically, and it lets a datetime bound stay a datetime.
// Folding to days made added__gt=...T10:00:00Z drop that whole afternoon.
func parseDateBounds(q url.Values, field dateFieldSpec) ([]dateBound, error) {
	var bounds []dateBound
	for _, spec := range dateSuffixes {
		name := field.param + spec.suffix
		raw := strings.TrimSpace(q.Get(name))
		if raw == "" {
			continue
		}
		if spec.dayOnly || !field.datetime {
			day, err := parseFilterDay(raw)
			if err != nil {
				return nil, fmt.Errorf("Invalid date %q for %q.", raw, name)
			}
			bounds = append(bounds, dateBound{op: spec.op, value: day.Format(dayLayout), day: true})
			continue
		}
		at, err := parseFilterTime(raw)
		if err != nil {
			return nil, fmt.Errorf("Invalid date %q for %q.", raw, name)
		}
		bounds = append(bounds, dateBound{op: spec.op, value: at.UTC().Format(storedTimeLayout)})
	}

	if raw := strings.TrimSpace(q.Get(field.param + "__year")); raw != "" {
		year, err := strconv.Atoi(raw)
		if err != nil || year < 1 || year > 9999 {
			return nil, fmt.Errorf("Invalid year %q for %q.", raw, field.param+"__year")
		}
		bounds = append(bounds,
			dateBound{op: ">=", value: fmt.Sprintf("%04d-01-01", year), day: true},
			dateBound{op: "<=", value: fmt.Sprintf("%04d-12-31", year), day: true},
		)
	}
	return bounds, nil
}

// parseFilterDay accepts the day itself or a full timestamp, which is what
// clients send interchangeably for the same filter.
func parseFilterDay(raw string) (time.Time, error) {
	if len(raw) > 10 {
		raw = raw[:10]
	}
	return time.Parse(dayLayout, raw)
}

// parseFilterTime reads an instant, or a bare day as its midnight.
func parseFilterTime(raw string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04",
		storedTimeLayout,
		"2006-01-02 15:04:05",
		dayLayout,
	} {
		if at, err := time.Parse(layout, raw); err == nil {
			return at, nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp %q", raw)
}

// createdValueSQL is the created date a client is shown: the document's own, or
// its upload day when it has none. mapDocument renders exactly this, so the
// filter and the sort must read it too -- otherwise every undated document is
// missing from a range covering the date on its own card.
const createdValueSQL = `COALESCE(NULLIF([[documents.document_date]], ''), [[documents.created]])`

// addedValueSQL is the upload timestamp, which paperless calls added.
const addedValueSQL = `[[documents.created]]`

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

	exprs = append(exprs, dateBoundExprs(createdValueSQL, "created", f.created, names)...)
	exprs = append(exprs, dateBoundExprs(addedValueSQL, "added", f.added, names)...)

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

// dateBoundExprs compares value against every bound. A day bound reads the
// first ten characters, because a date column is TEXT and holds both
// "YYYY-MM-DD" and "YYYY-MM-DD HH:MM:SS.sssZ". An empty value is excluded from
// any range rather than sorting below every bound.
func dateBoundExprs(value, prefix string, bounds []dateBound, names *paramNamer) []dbx.Expression {
	if len(bounds) == 0 {
		return nil
	}
	exprs := []dbx.Expression{
		dbx.NewExp(fmt.Sprintf("COALESCE(%s, '') != ''", value)),
	}
	for _, bound := range bounds {
		lhs := value
		if bound.day {
			lhs = fmt.Sprintf("substr(%s, 1, 10)", value)
		}
		placeholder, params := names.bindOne(prefix, bound.value)
		exprs = append(exprs, dbx.NewExp(
			fmt.Sprintf("%s %s %s", lhs, bound.op, placeholder), params,
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
