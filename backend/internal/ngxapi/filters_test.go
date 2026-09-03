package ngxapi

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"

	"lemmary/backend/internal/fulltext"
)

// seededIDs is an ngxIDs backed by a fixed table rather than a database, so
// the parser can be tested without an app: everything in it except id
// translation is pure, and this is the seam that keeps it that way.
func seededIDs(byCollection map[string]map[int]string) *ngxIDs {
	return &ngxIDs{
		memo: map[string]map[int]string{},
		lookup: func(collection string, ids []int) (map[int]string, error) {
			found := map[int]string{}
			for _, id := range ids {
				if pbID, ok := byCollection[collection][id]; ok {
					found[id] = pbID
				}
			}
			return found, nil
		},
	}
}

func tagIDs() *ngxIDs {
	return seededIDs(map[string]map[int]string{
		"tags":           {11: "tagone", 22: "tagtwo"},
		"document_types": {33: "typeone"},
		"correspondents": {44: "corrone"},
		"documents":      {55: "docone"},
	})
}

// testOwnerID is the caller's own client-facing id.
const testOwnerID = 7777

func mustParse(t *testing.T, query string) documentFilters {
	t.Helper()
	values, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse query %q: %v", query, err)
	}
	f, err := parseDocumentFiltersWith(tagIDs(), testOwnerID, values)
	if err != nil {
		t.Fatalf("parseDocumentFilters(%q): %v", query, err)
	}
	return f
}

// TestUnsupportedParamIsRefused is the whole point of the allowlist. Ignoring a
// filter the server cannot honour returns a 200 the client renders as though it
// had been applied, so "documents in this storage path" silently becomes "every
// document".
func TestUnsupportedParamIsRefused(t *testing.T) {
	t.Parallel()
	_, err := parseDocumentFiltersWith(tagIDs(), testOwnerID, url.Values{"storage_path__id": {"3"}})
	if err == nil {
		t.Fatal("an unsupported filter must be refused, not ignored")
	}
	if !strings.Contains(err.Error(), "storage_path__id") {
		t.Fatalf("error = %q, want it to name the parameter", err)
	}
}

func TestPagingParamsAreNotFilters(t *testing.T) {
	t.Parallel()
	if _, err := parseDocumentFiltersWith(tagIDs(), testOwnerID, url.Values{
		"page": {"2"}, "page_size": {"50"}, "ordering": {"-created"},
		"format": {"json"}, "full_perms": {"true"}, "truncate_content": {"true"},
		// Sent by the sweep that reconciles remote deletions.
		"fields": {"id"},
	}); err != nil {
		t.Fatalf("paging and shaping parameters must pass: %v", err)
	}
}

// TestUnresolvableIDInPositiveFilterIsImpossible pins the dangerous direction:
// asking for a tag nobody has must return nothing, never everything.
func TestUnresolvableIDInPositiveFilterIsImpossible(t *testing.T) {
	t.Parallel()
	for _, query := range []string{
		"tags__id__all=11,999",
		"tags__id__in=999",
		"document_type__id=999",
		"correspondent__id=999",
		"id__in=999",
	} {
		if f := mustParse(t, query); !f.impossible {
			t.Fatalf("%s: impossible = false, want true", query)
		}
	}
}

// TestUnresolvableIDInNoneFilterIsDropped is the mirror case: excluding a tag
// that does not exist excludes nothing, so the query stays answerable.
func TestUnresolvableIDInNoneFilterIsDropped(t *testing.T) {
	t.Parallel()
	f := mustParse(t, "tags__id__none=11,999")
	if f.impossible {
		t.Fatal("excluding an unknown tag must not make the query impossible")
	}
	if len(f.tagsNone) != 1 || f.tagsNone[0] != "tagone" {
		t.Fatalf("tagsNone = %v, want [tagone]", f.tagsNone)
	}
}

func TestIDsResolveToPocketBaseIDs(t *testing.T) {
	t.Parallel()
	f := mustParse(t, "tags__id__all=11&tags__id__in=22&document_type__id=33&correspondent__id=44&id=55")
	if len(f.tagsAll) != 1 || f.tagsAll[0] != "tagone" {
		t.Fatalf("tagsAll = %v, want [tagone]", f.tagsAll)
	}
	if len(f.tagsAny) != 1 || f.tagsAny[0] != "tagtwo" {
		t.Fatalf("tagsAny = %v, want [tagtwo]", f.tagsAny)
	}
	if len(f.docTypes) != 1 || f.docTypes[0] != "typeone" {
		t.Fatalf("docTypes = %v, want [typeone]", f.docTypes)
	}
	if len(f.corrs) != 1 || f.corrs[0] != "corrone" {
		t.Fatalf("corrs = %v, want [corrone]", f.corrs)
	}
	if len(f.ids) != 1 || f.ids[0] != "docone" {
		t.Fatalf("ids = %v, want [docone]", f.ids)
	}
}

func TestTextCriteriaCarryTheirFields(t *testing.T) {
	t.Parallel()
	f := mustParse(t, "query=lease&title_content=rent&title__icontains=deed&content__icontains=clause")
	if len(f.text) != 4 {
		t.Fatalf("text = %v, want four criteria", f.text)
	}
	// The general query comes first: when several criteria are present its
	// ranking is the one the page is ordered by.
	if f.text[0].text != "lease" || f.text[0].fields != nil {
		t.Fatalf("first criterion = %+v, want the unrestricted query", f.text[0])
	}
	if got, want := len(f.text[1].fields), 3; got != want {
		t.Fatalf("title_content fields = %d, want %d", got, want)
	}
	if f.text[3].fields[0] != fulltext.FieldOCRText {
		t.Fatalf("content__icontains fields = %v, want the OCR field", f.text[3].fields)
	}
}

func TestDateComparatorsBecomeBounds(t *testing.T) {
	t.Parallel()
	cases := map[string][]dateBound{
		"created__date__gte=2025-01-01": {{op: ">=", value: "2025-01-01", day: true}},
		"created__date__gt=2025-01-01":  {{op: ">", value: "2025-01-01", day: true}},
		"created__date__lte=2025-12-31": {{op: "<=", value: "2025-12-31", day: true}},
		"created__date__lt=2025-12-31":  {{op: "<", value: "2025-12-31", day: true}},
		"created__year=2025": {
			{op: ">=", value: "2025-01-01", day: true},
			{op: "<=", value: "2025-12-31", day: true},
		},
		// document_date carries no time, so this can only mean the day.
		"created__gte=2025-03-01T10:00:00Z": {{op: ">=", value: "2025-03-01", day: true}},
		"created__date__gt=2025-01-01&created__gte=2025-06-01": {
			{op: ">", value: "2025-01-01", day: true},
			{op: ">=", value: "2025-06-01", day: true},
		},
	}
	for query, want := range cases {
		if got := mustParse(t, query).created; !sameBounds(got, want) {
			t.Fatalf("%s: created = %+v, want %+v", query, got, want)
		}
	}

	added := mustParse(t, "added__date__gte=2024-02-03")
	if want := []dateBound{{op: ">=", value: "2024-02-03", day: true}}; !sameBounds(added.added, want) {
		t.Fatalf("added.added = %+v, want %+v", added.added, want)
	}
	if len(added.created) != 0 {
		t.Fatalf("added filters must not land on the document date: %+v", added.created)
	}
}

// TestAddedComparatorsKeepTheirTime: folding these to days made "everything
// since my last poll" skip every upload made earlier the same day.
func TestAddedComparatorsKeepTheirTime(t *testing.T) {
	t.Parallel()
	cases := map[string][]dateBound{
		"added__gt=2025-06-15T10:00:00Z":  {{op: ">", value: "2025-06-15 10:00:00.000Z"}},
		"added__lte=2025-06-15T23:59:00Z": {{op: "<=", value: "2025-06-15 23:59:00.000Z"}},
		// Normalised to UTC, which is what the column stores.
		"added__gte=2025-06-15T12:00:00%2B02:00": {{op: ">=", value: "2025-06-15 10:00:00.000Z"}},
		// A bare day is that day's midnight, the way Django reads it.
		"added__gt=2025-06-15": {{op: ">", value: "2025-06-15 00:00:00.000Z"}},
		// The __date__ forms still mean the day.
		"added__date__gt=2025-06-15": {{op: ">", value: "2025-06-15", day: true}},
	}
	for query, want := range cases {
		if got := mustParse(t, query).added; !sameBounds(got, want) {
			t.Fatalf("%s: added = %+v, want %+v", query, got, want)
		}
	}
}

// TestAddedYearIsAccepted is the parameter the allowlist and the parser had
// already drifted on: it parsed correctly and was refused with a 400.
func TestAddedYearIsAccepted(t *testing.T) {
	t.Parallel()
	want := []dateBound{
		{op: ">=", value: "2025-01-01", day: true},
		{op: "<=", value: "2025-12-31", day: true},
	}
	if got := mustParse(t, "added__year=2025").added; !sameBounds(got, want) {
		t.Fatalf("added__year = %+v, want %+v", got, want)
	}
}

// TestOwnerFiltersAreAnsweredNotRefused: refusing the owner pill turned "My
// documents" into an error.
func TestOwnerFiltersAreAnsweredNotRefused(t *testing.T) {
	t.Parallel()
	mine := fmt.Sprint(testOwnerID)
	for _, query := range []string{
		"owner__id=" + mine,
		"owner__id__in=" + mine,
		"owner__id__in=" + mine + ",999",
		"owner__id__none=999",
		"owner__isnull=false",
	} {
		if f := mustParse(t, query); f.impossible {
			t.Fatalf("%s: impossible = true, want the caller's own documents", query)
		}
	}
	for _, query := range []string{
		"owner__id=999",
		"owner__id__in=999",
		"owner__id__none=" + mine,
		"owner__isnull=true",
	} {
		if f := mustParse(t, query); !f.impossible {
			t.Fatalf("%s: impossible = false, want nothing to match", query)
		}
	}
}

// dayBounds is the inclusive [from, to] pair of days most filters mean.
func dayBounds(from, to string) []dateBound {
	var bounds []dateBound
	if from != "" {
		bounds = append(bounds, dateBound{op: ">=", value: from, day: true})
	}
	if to != "" {
		bounds = append(bounds, dateBound{op: "<=", value: to, day: true})
	}
	return bounds
}

func sameBounds(got, want []dateBound) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestInvalidValuesAreRefused(t *testing.T) {
	t.Parallel()
	for _, query := range []string{
		"created__date__gt=not-a-date",
		"is_tagged=maybe",
		"tags__id__all=abc",
		"created__year=20xx",
	} {
		values, _ := url.ParseQuery(query)
		if _, err := parseDocumentFiltersWith(tagIDs(), testOwnerID, values); err == nil {
			t.Fatalf("%s: want an error", query)
		}
	}
}

// TestStoragePathIsNullFalseMatchesNothing: Lemmary has no storage paths, so
// every document is without one. Asking for the documents that have one is
// answerable, and the answer is none.
func TestStoragePathIsNullFalseMatchesNothing(t *testing.T) {
	t.Parallel()
	if f := mustParse(t, "storage_path__isnull=false"); !f.impossible {
		t.Fatal("storage_path__isnull=false must match nothing")
	}
	if f := mustParse(t, "storage_path__isnull=true"); f.impossible {
		t.Fatal("storage_path__isnull=true must match everything")
	}
}

// filterDB is a documents table in the shapes PocketBase actually writes: tags
// as a JSON array, as the legacy empty string, and as NULL; relations both
// empty and NULL; dates in both stored formats and blank.
func filterDB(t *testing.T) *dbx.DB {
	t.Helper()
	db, err := dbx.Open("sqlite", "file:"+t.Name()+"?mode=memory&cache=shared&_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := db.NewQuery(`CREATE TABLE documents (
		id TEXT PRIMARY KEY, user TEXT, tags TEXT,
		document_type TEXT, correspondent TEXT,
		document_date TEXT, created TEXT)`).Execute(); err != nil {
		t.Fatalf("create table: %v", err)
	}

	rows := []struct{ id, tags, typ, corr, date, created string }{
		{"both", `["t1","t2"]`, "ti", "co", "2025-01-15", "2025-01-16 09:00:00.000Z"},
		{"one", `["t1"]`, "ti", "", "2025-03-02 00:00:00.000Z", "2025-03-03 09:00:00.000Z"},
		{"other", `["t2"]`, "tx", "co", "2025-11-30", "2025-12-01 09:00:00.000Z"},
		{"empty", `[]`, "", "", "2024-12-31", "2025-01-02 09:00:00.000Z"},
		{"legacy", ``, "", "", "", "2025-01-03 09:00:00.000Z"},
	}
	for _, r := range rows {
		if _, err := db.NewQuery(`INSERT INTO documents
			(id, user, tags, document_type, correspondent, document_date, created)
			VALUES ({:id}, 'me', {:tags}, {:typ}, {:corr}, {:date}, {:created})`).Bind(dbx.Params{
			"id": r.id, "tags": r.tags, "typ": r.typ, "corr": r.corr,
			"date": r.date, "created": r.created,
		}).Execute(); err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	// The NULL row cannot be bound as a Go string, so it is written separately.
	if _, err := db.NewQuery(`INSERT INTO documents
		(id, user, tags, document_type, correspondent, document_date, created)
		VALUES ('null', 'me', NULL, NULL, NULL, NULL, '2025-01-04 09:00:00.000Z')`).Execute(); err != nil {
		t.Fatalf("insert null row: %v", err)
	}
	return db
}

func matchingIDs(t *testing.T, db *dbx.DB, f documentFilters) []string {
	t.Helper()
	q := db.Select("id").From("documents")
	for _, expr := range documentFilterExprs(f) {
		q.AndWhere(expr)
	}
	var ids []string
	if err := q.OrderBy("id").Column(&ids); err != nil {
		t.Fatalf("run filter: %v", err)
	}
	return ids
}

func assertIDs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ids = %v, want %v", got, want)
	}
}

func TestTagsAllRequiresEveryTag(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{tagsAll: []string{"t1", "t2"}}), "both")
	assertIDs(t, matchingIDs(t, db, documentFilters{tagsAll: []string{"t1"}}), "both", "one")
}

func TestTagsAnyMatchesAnyTag(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{tagsAny: []string{"t1", "t2"}}), "both", "one", "other")
}

// TestTagsNoneKeepsUntaggedRows pins the NULL handling: json_valid(NULL) is
// NULL, so a negation written as NOT (json_valid(tags) AND ...) evaluates to
// NULL and silently drops the row that has no tags column value at all --
// exactly the row an exclusion filter is most obviously right about.
func TestTagsNoneKeepsUntaggedRows(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{tagsNone: []string{"t1"}}),
		"empty", "legacy", "null", "other")
}

func TestIsTaggedSplitsTheArchive(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	yes, no := true, false
	assertIDs(t, matchingIDs(t, db, documentFilters{isTagged: &yes}), "both", "one", "other")
	assertIDs(t, matchingIDs(t, db, documentFilters{isTagged: &no}), "empty", "legacy", "null")
}

// TestRelationNoneKeepsRowsWithNoRelation pins the COALESCE: a bare NOT IN
// drops NULL under SQL three-valued logic, but a document with no document type
// at all is plainly not of the excluded type.
func TestRelationNoneKeepsRowsWithNoRelation(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{docTypesNone: []string{"ti"}}),
		"empty", "legacy", "null", "other")
}

func TestRelationFiltersAndUnsetChecks(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	yes, no := true, false
	assertIDs(t, matchingIDs(t, db, documentFilters{docTypes: []string{"ti"}}), "both", "one")
	assertIDs(t, matchingIDs(t, db, documentFilters{corrs: []string{"co"}}), "both", "other")
	assertIDs(t, matchingIDs(t, db, documentFilters{docTypeUnset: &yes}), "empty", "legacy", "null")
	assertIDs(t, matchingIDs(t, db, documentFilters{docTypeUnset: &no}), "both", "one", "other")
}

// TestCreatedRangeUsesTheDateTheClientSees: an undated document is rendered
// with its upload date, so the filter has to read the same fallback. Comparing
// document_date alone dropped it from every range, including one covering the
// date on its own card.
func TestCreatedRangeUsesTheDateTheClientSees(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	// legacy and null carry no date, so they answer on their upload day.
	assertIDs(t, matchingIDs(t, db, documentFilters{created: dayBounds("", "2025-12-31")}),
		"both", "empty", "legacy", "null", "one", "other")
	// And out of a range ending before they were uploaded.
	assertIDs(t, matchingIDs(t, db, documentFilters{created: dayBounds("", "2025-01-02")}),
		"empty")
}

// TestDateRangeMatchesBothStoredFormats: document_date holds "YYYY-MM-DD" and
// "YYYY-MM-DD HH:MM:SS.sssZ" side by side, which is why the comparison is on
// the first ten characters.
func TestDateRangeMatchesBothStoredFormats(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	// legacy and null answer on their upload day, the 3rd and the 4th.
	assertIDs(t, matchingIDs(t, db, documentFilters{created: dayBounds("2025-01-01", "2025-03-31")}),
		"both", "legacy", "null", "one")
	assertIDs(t, matchingIDs(t, db, documentFilters{added: dayBounds("2025-03-01", "")}), "one", "other")
}

// TestAddedTimestampBoundsCompareTheWholeInstant: these rows were uploaded at
// 09:00, so bounds either side of that morning must split them -- where a
// day-truncated bound would have moved by a whole day.
func TestAddedTimestampBoundsCompareTheWholeInstant(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{
		added: []dateBound{{op: ">", value: "2025-03-03 08:00:00.000Z"}},
	}), "one", "other")
	assertIDs(t, matchingIDs(t, db, documentFilters{
		added: []dateBound{{op: ">", value: "2025-03-03 10:00:00.000Z"}},
	}), "other")
}

// TestSeveralFiltersGetDistinctPlaceholders guards the shared parameter map:
// dbx merges every expression's params into one, so two filters reusing a name
// would overwrite each other and one of them would silently stop applying.
func TestSeveralFiltersGetDistinctPlaceholders(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{
		tagsAll:      []string{"t1", "t2"},
		tagsNone:     []string{"tz"},
		docTypesNone: []string{"tx"},
		created:      dayBounds("2025-01-01", "2025-06-30"),
		added:        dayBounds("2025-01-01", ""),
	}), "both")
}

func TestImpossibleFilterSetMatchesNothing(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{impossible: true}))
}

func TestDocumentOrderAlwaysBreaksTies(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		columns  []string
		dbSorted bool
	}{
		"":         {newestFirst(), false},
		"-created": {[]string{createdValueSQL + " DESC", "[[documents.id]] DESC"}, true},
		"title":    {[]string{"[[documents.title]] ASC", "[[documents.id]] ASC"}, true},
		"added":    {[]string{"[[documents.created]] ASC", "[[documents.id]] ASC"}, true},
		"modified": {[]string{"[[documents.updated]] ASC", "[[documents.id]] ASC"}, true},
		"id":       {[]string{"[[documents.ngx_id]] ASC", "[[documents.id]] ASC"}, true},
		// Relevance and nonsense are both "the database cannot serve this".
		"-score":   {newestFirst(), false},
		"score":    {newestFirst(), false},
		"nonsense": {newestFirst(), false},
	}
	for ordering, want := range cases {
		got, dbSorted := documentOrder(ordering)
		if strings.Join(got, "|") != strings.Join(want.columns, "|") || dbSorted != want.dbSorted {
			t.Fatalf("documentOrder(%q) = %v, %v; want %v, %v",
				ordering, got, dbSorted, want.columns, want.dbSorted)
		}
	}
}

func TestTruncateContentCutsOnRuneBoundary(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("é", truncatedContentLen+50)
	got := truncateContent(long)
	if n := len([]rune(got)); n != truncatedContentLen {
		t.Fatalf("truncated to %d runes, want %d", n, truncatedContentLen)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatal("truncation split a multi-byte rune")
	}
	if short := truncateContent("brief"); short != "brief" {
		t.Fatalf("short content changed: %q", short)
	}
}
