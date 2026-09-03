package ngxapi

import (
	"net/url"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"

	"lemmary/backend/internal/fulltext"
)

// seededIDs is an ngxIDs whose cache is already warm, so the parser can be
// tested without an app: load() short-circuits on a populated collection.
func seededIDs(byCollection map[string]map[int]string) *ngxIDs {
	return &ngxIDs{byCollection: byCollection}
}

func tagIDs() *ngxIDs {
	return seededIDs(map[string]map[int]string{
		"tags":           {11: "tagone", 22: "tagtwo"},
		"document_types": {33: "typeone"},
		"correspondents": {44: "corrone"},
		"documents":      {55: "docone"},
	})
}

func mustParse(t *testing.T, query string) documentFilters {
	t.Helper()
	values, err := url.ParseQuery(query)
	if err != nil {
		t.Fatalf("parse query %q: %v", query, err)
	}
	f, err := parseDocumentFiltersWith(tagIDs(), values)
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
	_, err := parseDocumentFiltersWith(tagIDs(), url.Values{"storage_path__id": {"3"}})
	if err == nil {
		t.Fatal("an unsupported filter must be refused, not ignored")
	}
	if !strings.Contains(err.Error(), "storage_path__id") {
		t.Fatalf("error = %q, want it to name the parameter", err)
	}
}

func TestPagingParamsAreNotFilters(t *testing.T) {
	t.Parallel()
	if _, err := parseDocumentFiltersWith(tagIDs(), url.Values{
		"page": {"2"}, "page_size": {"50"}, "ordering": {"-created"},
		"format": {"json"}, "full_perms": {"true"}, "truncate_content": {"true"},
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

func TestDateRangeFoldsEveryComparator(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ from, to string }{
		"created__date__gte=2025-01-01":                        {"2025-01-01", ""},
		"created__date__gt=2025-01-01":                         {"2025-01-02", ""},
		"created__date__lte=2025-12-31":                        {"", "2025-12-31"},
		"created__date__lt=2025-12-31":                         {"", "2025-12-30"},
		"created__year=2025":                                   {"2025-01-01", "2025-12-31"},
		"created__gte=2025-03-01T10:00:00Z":                    {"2025-03-01", ""},
		"created__date__gt=2025-01-01&created__gte=2025-06-01": {"2025-06-01", ""},
	}
	for query, want := range cases {
		f := mustParse(t, query)
		if f.createdFrom != want.from || f.createdTo != want.to {
			t.Fatalf("%s: got [%s, %s], want [%s, %s]", query, f.createdFrom, f.createdTo, want.from, want.to)
		}
	}

	added := mustParse(t, "added__date__gte=2024-02-03")
	if added.addedFrom != "2024-02-03" || added.createdFrom != "" {
		t.Fatalf("added filters must not land on document_date: %+v", added)
	}
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
		if _, err := parseDocumentFiltersWith(tagIDs(), values); err == nil {
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

// TestDateRangeExcludesUndatedDocuments: a blank date must not sort below every
// lower bound and slip into every range.
func TestDateRangeExcludesUndatedDocuments(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{createdTo: "2025-12-31"}),
		"both", "empty", "one", "other")
}

// TestDateRangeMatchesBothStoredFormats: document_date holds "YYYY-MM-DD" and
// "YYYY-MM-DD HH:MM:SS.sssZ" side by side, which is why the comparison is on
// the first ten characters.
func TestDateRangeMatchesBothStoredFormats(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{createdFrom: "2025-01-01", createdTo: "2025-03-31"}),
		"both", "one")
	assertIDs(t, matchingIDs(t, db, documentFilters{addedFrom: "2025-03-01"}), "one", "other")
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
		createdFrom:  "2025-01-01",
		createdTo:    "2025-06-30",
		addedFrom:    "2025-01-01",
	}), "both")
}

func TestImpossibleFilterSetMatchesNothing(t *testing.T) {
	t.Parallel()
	db := filterDB(t)
	assertIDs(t, matchingIDs(t, db, documentFilters{impossible: true}))
}

func TestDocumentSortColumnsAlwaysBreakTies(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"":         {"[[documents.created]] DESC", "[[documents.id]] DESC"},
		"-created": {"[[documents.document_date]] DESC", "[[documents.id]] DESC"},
		"title":    {"[[documents.title]] ASC", "[[documents.id]] ASC"},
		"added":    {"[[documents.created]] ASC", "[[documents.id]] ASC"},
		"modified": {"[[documents.updated]] ASC", "[[documents.id]] ASC"},
		"id":       {"[[documents.id]] ASC"},
		"nonsense": {"[[documents.created]] ASC", "[[documents.id]] ASC"},
	}
	for ordering, want := range cases {
		got := documentSortColumns(ordering)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("documentSortColumns(%q) = %v, want %v", ordering, got, want)
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
