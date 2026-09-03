package ngxapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/ngxid"

	_ "lemmary/backend/migrations"
)

// bootSchemaTestApp is bootTestApp plus the Lemmary schema. Bootstrap runs only
// PocketBase's system migrations, so documents, tags and the rest do not exist
// until the app migrations run too.
//
// It binds ngxid.Register for the same reason appwire does: a record created
// without it carries no client-facing id, and nothing in this package could
// then address it.
func bootSchemaTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := bootTestApp(t)
	if err := app.RunAppMigrations(); err != nil {
		t.Fatalf("run app migrations: %v", err)
	}
	ngxid.Register(app)
	return app
}

// listFixture is one owner with two tags and three documents, plus a second
// owner whose document must never appear.
type listFixture struct {
	app      *pocketbase.PocketBase
	userID   string
	otherID  string
	tagOne   string
	tagTwo   string
	docBoth  string
	docOne   string
	docNone  string
	docTheir string
}

func newListFixture(t *testing.T) listFixture {
	t.Helper()
	app := bootSchemaTestApp(t)
	f := listFixture{app: app}

	f.userID = createUser(t, app, "owner@example.com")
	f.otherID = createUser(t, app, "other@example.com")
	f.tagOne = createNamed(t, app, "tags", "invoice", f.userID)
	f.tagTwo = createNamed(t, app, "tags", "paid", f.userID)

	f.docBoth = createDocument(t, app, f.userID, "Both", "2025-01-15", []string{f.tagOne, f.tagTwo})
	f.docOne = createDocument(t, app, f.userID, "One", "2025-06-15", []string{f.tagOne})
	f.docNone = createDocument(t, app, f.userID, "None", "2025-11-15", nil)
	f.docTheir = createDocument(t, app, f.otherID, "Theirs", "2025-01-15", []string{})
	return f
}

func createUser(t *testing.T, app core.App, email string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("email", email)
	record.SetPassword("test-password-123")
	if err := app.Save(record); err != nil {
		t.Fatalf("save user %s: %v", email, err)
	}
	return record.Id
}

func createNamed(t *testing.T, app core.App, collection, name, userID string) string {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("%s collection: %v", collection, err)
	}
	record := core.NewRecord(coll)
	record.Set("name", name)
	record.Set("user", userID)
	if coll.Fields.GetByName("name_original") != nil {
		record.Set("name_original", name)
	}
	if err := app.Save(record); err != nil {
		t.Fatalf("save %s %s: %v", collection, name, err)
	}
	return record.Id
}

func createDocument(t *testing.T, app core.App, userID, title, date string, tags []string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("title", title)
	record.Set("document_date", date)
	// The file field is required, and each document needs its own bytes: the
	// pipeline rejects an exact checksum duplicate.
	file, err := filesystem.NewFileFromBytes([]byte("document "+title), title+".txt")
	if err != nil {
		t.Fatalf("build file for %s: %v", title, err)
	}
	record.Set("file", file)
	if tags != nil {
		record.Set("tags", tags)
	}
	if err := app.Save(record); err != nil {
		t.Fatalf("save document %s: %v", title, err)
	}
	return record.Id
}

type listResponse struct {
	Count   int              `json:"count"`
	Next    *string          `json:"next"`
	Results []map[string]any `json:"results"`
}

// list calls the handler directly, the way the rest of this package's handler
// tests do: no router, no server.
func (f listFixture) list(t *testing.T, query string) (int, listResponse) {
	t.Helper()
	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodGet, "/api/documents/?"+query, nil)
	e.Response = httptest.NewRecorder()

	user, err := f.app.FindRecordById("users", f.userID)
	if err != nil {
		t.Fatalf("load auth user: %v", err)
	}
	e.Auth = user

	if err := handleListDocuments(nil)(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	var body listResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, body
}

func (f listFixture) titles(results []map[string]any) []string {
	titles := make([]string, 0, len(results))
	for _, r := range results {
		titles = append(titles, fmt.Sprint(r["title"]))
	}
	return titles
}

func TestListDocumentsAppliesTagFilters(t *testing.T) {
	f := newListFixture(t)

	status, all := f.list(t, "")
	if status != http.StatusOK || all.Count != 3 {
		t.Fatalf("unfiltered: status %d count %d, want 200 and the owner's 3 documents", status, all.Count)
	}

	_, both := f.list(t, fmt.Sprintf("tags__id__all=%d,%d", toNgxID(f.tagOne), toNgxID(f.tagTwo)))
	if got := f.titles(both.Results); len(got) != 1 || got[0] != "Both" {
		t.Fatalf("tags__id__all = %v, want [Both]", got)
	}

	_, any := f.list(t, fmt.Sprintf("tags__id__in=%d,%d", toNgxID(f.tagOne), toNgxID(f.tagTwo)))
	if any.Count != 2 {
		t.Fatalf("tags__id__in count = %d, want 2", any.Count)
	}

	_, none := f.list(t, fmt.Sprintf("tags__id__none=%d", toNgxID(f.tagOne)))
	if got := f.titles(none.Results); len(got) != 1 || got[0] != "None" {
		t.Fatalf("tags__id__none = %v, want [None]", got)
	}

	_, untagged := f.list(t, "is_tagged=false")
	if got := f.titles(untagged.Results); len(got) != 1 || got[0] != "None" {
		t.Fatalf("is_tagged=false = %v, want [None]", got)
	}
}

func TestListDocumentsAppliesDateRange(t *testing.T) {
	f := newListFixture(t)

	_, ranged := f.list(t, "created__date__gte=2025-02-01&created__date__lte=2025-07-01")
	if got := f.titles(ranged.Results); len(got) != 1 || got[0] != "One" {
		t.Fatalf("date range = %v, want [One]", got)
	}

	// gt is strict: the document dated exactly on the bound is excluded.
	_, strict := f.list(t, "created__date__gt=2025-06-15")
	if got := f.titles(strict.Results); len(got) != 1 || got[0] != "None" {
		t.Fatalf("created__date__gt = %v, want [None]", got)
	}
}

// TestListDocumentsCountMatchesTheFilteredPage is the invariant that made the
// count and the page share one expression: a count taken from a different
// query than the rows it counts sends the client paging into emptiness.
func TestListDocumentsCountMatchesTheFilteredPage(t *testing.T) {
	f := newListFixture(t)

	_, page := f.list(t, fmt.Sprintf("tags__id__all=%d&page_size=1", toNgxID(f.tagOne)))
	if page.Count != 2 {
		t.Fatalf("count = %d, want the 2 tagged documents", page.Count)
	}
	if len(page.Results) != 1 {
		t.Fatalf("results = %d, want the requested page size of 1", len(page.Results))
	}
	if page.Next == nil {
		t.Fatal("a second page exists but no next link was returned")
	}
}

// TestListDocumentsUnknownIDReturnsNothing pins the dangerous direction. Before
// filters were parsed at all, every one of these returned the whole archive.
func TestListDocumentsUnknownIDReturnsNothing(t *testing.T) {
	f := newListFixture(t)

	for _, query := range []string{
		"tags__id__all=123456",
		"document_type__id=123456",
		"correspondent__id=123456",
		// Another owner's tag must not resolve for this caller.
		fmt.Sprintf("tags__id__all=%d", toNgxID(createNamed(t, f.app, "tags", "theirs", f.otherID))),
	} {
		status, body := f.list(t, query)
		if status != http.StatusOK {
			t.Fatalf("%s: status = %d, want 200", query, status)
		}
		if body.Count != 0 || len(body.Results) != 0 {
			t.Fatalf("%s: returned %d documents, want none", query, body.Count)
		}
	}
}

func TestListDocumentsRefusesUnsupportedFilter(t *testing.T) {
	f := newListFixture(t)

	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodGet, "/api/documents/?storage_path__id=3", nil)
	e.Response = httptest.NewRecorder()
	user, _ := f.app.FindRecordById("users", f.userID)
	e.Auth = user

	if err := handleListDocuments(nil)(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Detail == "" {
		t.Fatalf("no detail in %s", rec.Body.String())
	}
}

// TestListDocumentsPagesWithoutRepeatingRows pins the id tiebreaker: these
// documents share a created timestamp, and without a total order SQLite may
// return tied rows in a different order per page, showing one row twice and
// another never.
func TestListDocumentsPagesWithoutRepeatingRows(t *testing.T) {
	f := newListFixture(t)

	seen := map[string]bool{}
	for page := 1; page <= 3; page++ {
		_, body := f.list(t, fmt.Sprintf("ordering=-added&page_size=1&page=%d", page))
		if len(body.Results) != 1 {
			t.Fatalf("page %d returned %d documents, want 1", page, len(body.Results))
		}
		id := fmt.Sprint(body.Results[0]["id"])
		if seen[id] {
			t.Fatalf("page %d repeated document %s", page, id)
		}
		seen[id] = true
	}
	if len(seen) != 3 {
		t.Fatalf("paged over %d distinct documents, want 3", len(seen))
	}
}

// TestListDocumentsTruncatesContentOnRequest: swift-paperless asks for this on
// list views, where the full OCR text of every hit is most of the payload.
func TestListDocumentsTruncatesContentOnRequest(t *testing.T) {
	f := newListFixture(t)

	doc, err := f.app.FindRecordById("documents", f.docNone)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	long := ""
	for len(long) <= truncatedContentLen*2 {
		long += "the quick brown fox jumps over the lazy dog. "
	}
	doc.Set("ocr_text", long)
	if err := f.app.Save(doc); err != nil {
		t.Fatalf("save ocr text: %v", err)
	}

	_, full := f.list(t, "is_tagged=false")
	if got := len(fmt.Sprint(full.Results[0]["content"])); got <= truncatedContentLen {
		t.Fatalf("untruncated content is %d chars, want the full text", got)
	}

	_, cut := f.list(t, "is_tagged=false&truncate_content=true")
	if got := len(fmt.Sprint(cut.Results[0]["content"])); got != truncatedContentLen {
		t.Fatalf("truncated content is %d chars, want %d", got, truncatedContentLen)
	}
}

func TestListDocumentsAppliesTaxonomyFilters(t *testing.T) {
	f := newListFixture(t)

	invoiceType := createNamed(t, f.app, "document_types", "Invoice", f.userID)
	acme := createNamed(t, f.app, "correspondents", "Acme", f.userID)

	doc, err := f.app.FindRecordById("documents", f.docOne)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	doc.Set("document_type", invoiceType)
	doc.Set("correspondent", acme)
	if err := f.app.Save(doc); err != nil {
		t.Fatalf("assign taxonomy: %v", err)
	}

	_, byType := f.list(t, fmt.Sprintf("document_type__id=%d", toNgxID(invoiceType)))
	if got := f.titles(byType.Results); len(got) != 1 || got[0] != "One" {
		t.Fatalf("document_type__id = %v, want [One]", got)
	}

	_, byCorr := f.list(t, fmt.Sprintf("correspondent__id__in=%d", toNgxID(acme)))
	if got := f.titles(byCorr.Results); len(got) != 1 || got[0] != "One" {
		t.Fatalf("correspondent__id__in = %v, want [One]", got)
	}

	_, unset := f.list(t, "document_type__isnull=true")
	if unset.Count != 2 {
		t.Fatalf("document_type__isnull=true count = %d, want the 2 untyped documents", unset.Count)
	}

	_, combined := f.list(t, fmt.Sprintf("document_type__id=%d&correspondent__isnull=true",
		toNgxID(invoiceType)))
	if combined.Count != 0 {
		t.Fatalf("contradictory filters returned %d documents, want none", combined.Count)
	}
}

// indexFixture puts the owner's documents into a real Bleve index, so the
// search path can be exercised the way a client reaches it.
func (f listFixture) indexed(t *testing.T) *fulltext.Index {
	t.Helper()
	idx := fulltext.New()
	if err := idx.Open(t.TempDir()); err != nil {
		t.Fatalf("open index: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })

	for _, id := range []string{f.docBoth, f.docOne, f.docNone, f.docTheir} {
		record, err := f.app.FindRecordById("documents", id)
		if err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		if err := idx.Put(record.Id, fulltext.Build(f.app, record)); err != nil {
			t.Fatalf("index %s: %v", id, err)
		}
	}
	return idx
}

func (f listFixture) search(t *testing.T, idx *fulltext.Index, query string) listResponse {
	t.Helper()
	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodGet, "/api/documents/?"+query, nil)
	e.Response = httptest.NewRecorder()
	user, err := f.app.FindRecordById("users", f.userID)
	if err != nil {
		t.Fatalf("load auth user: %v", err)
	}
	e.Auth = user

	if err := handleListDocuments(idx)(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body listResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return body
}

// TestListDocumentsIntersectsSearchWithFilters is the combination that has to
// work for a client's search box and its filter chips to agree: the index
// answers the words, the database answers everything else, and the page is the
// intersection with a count that matches it.
func TestListDocumentsIntersectsSearchWithFilters(t *testing.T) {
	f := newListFixture(t)
	idx := f.indexed(t)

	titles := f.search(t, idx, "query=One")
	if got := f.titles(titles.Results); len(got) != 1 || got[0] != "One" {
		t.Fatalf("query = %v, want [One]", got)
	}

	// Same words, plus a tag the match does not carry: nothing survives.
	none := f.search(t, idx, fmt.Sprintf("query=One&tags__id__all=%d", toNgxID(f.tagTwo)))
	if none.Count != 0 || len(none.Results) != 0 {
		t.Fatalf("search intersected with a non-matching tag returned %d, want none", none.Count)
	}

	// And a tag it does carry: the match survives, and the count agrees.
	kept := f.search(t, idx, fmt.Sprintf("query=One&tags__id__all=%d", toNgxID(f.tagOne)))
	if kept.Count != 1 || len(kept.Results) != 1 {
		t.Fatalf("count = %d with %d results, want 1 and 1", kept.Count, len(kept.Results))
	}

	// A search must never reach another owner's documents.
	theirs := f.search(t, idx, "query=Theirs")
	if theirs.Count != 0 {
		t.Fatalf("search returned %d of another owner's documents", theirs.Count)
	}
}

// TestListDocumentsTitleContentSearchesTitleAndBody is what swift-paperless
// sends from its default search mode, and what returned the whole archive
// before the filter was read at all.
func TestListDocumentsTitleContentSearchesTitleAndBody(t *testing.T) {
	f := newListFixture(t)

	doc, err := f.app.FindRecordById("documents", f.docNone)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	doc.Set("ocr_text", "a lease agreement for the warehouse")
	if err := f.app.Save(doc); err != nil {
		t.Fatalf("save ocr text: %v", err)
	}
	idx := f.indexed(t)

	body := f.search(t, idx, "title_content=lease")
	if got := f.titles(body.Results); len(got) != 1 || got[0] != "None" {
		t.Fatalf("title_content matched %v, want the document whose body says lease", got)
	}

	byTitle := f.search(t, idx, "title_content=Both")
	if got := f.titles(byTitle.Results); len(got) != 1 || got[0] != "Both" {
		t.Fatalf("title_content matched %v, want the document whose title says Both", got)
	}

	// content__icontains must not reach the title.
	titleOnly := f.search(t, idx, "content__icontains=Both")
	if titleOnly.Count != 0 {
		t.Fatalf("content__icontains matched %d documents on their title alone", titleOnly.Count)
	}
}

// TestListRendersStoredRelationIDs: the ids in a response have to be the ones
// the client can send back. They come from the related rows now rather than
// from a hash of a PocketBase id, so a document's tags, type and correspondent
// must round-trip through the filters that resolve them.
func TestListRendersStoredRelationIDs(t *testing.T) {
	f := newListFixture(t)

	invoiceType := createNamed(t, f.app, "document_types", "Invoice", f.userID)
	acme := createNamed(t, f.app, "correspondents", "Acme", f.userID)
	doc, err := f.app.FindRecordById("documents", f.docOne)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	doc.Set("document_type", invoiceType)
	doc.Set("correspondent", acme)
	if err := f.app.Save(doc); err != nil {
		t.Fatalf("assign taxonomy: %v", err)
	}

	_, body := f.list(t, "is_tagged=true&tags__id__all="+fmt.Sprint(storedID(t, f.app, "tags", f.tagOne)))
	var one map[string]any
	for _, result := range body.Results {
		if result["title"] == "One" {
			one = result
		}
	}
	if one == nil {
		t.Fatalf("document One is missing from %v", f.titles(body.Results))
	}

	if got, want := int(one["id"].(float64)), storedID(t, f.app, "documents", f.docOne); got != want {
		t.Fatalf("document id = %d, want the stored %d", got, want)
	}
	if got, want := int(one["document_type"].(float64)), storedID(t, f.app, "document_types", invoiceType); got != want {
		t.Fatalf("document_type = %d, want the stored %d", got, want)
	}
	if got, want := int(one["correspondent"].(float64)), storedID(t, f.app, "correspondents", acme); got != want {
		t.Fatalf("correspondent = %d, want the stored %d", got, want)
	}
	tags, _ := one["tags"].([]any)
	if len(tags) != 1 || int(tags[0].(float64)) != storedID(t, f.app, "tags", f.tagOne) {
		t.Fatalf("tags = %v, want the stored id of the one tag", tags)
	}
}

// TestRenderingAPageBatchesRelationLookups is the cost of reading ids from
// related rows instead of hashing them. Per field it would be one query per tag
// per document -- the same per-row traffic the stored id was added to remove.
func TestRenderingAPageBatchesRelationLookups(t *testing.T) {
	f := newListFixture(t)

	queries := countTableQueries(t, f.app, "tags")
	if _, body := f.list(t, ""); len(body.Results) != 3 {
		t.Fatalf("listed %d documents, want 3", len(body.Results))
	}
	if n := queries(); n != 1 {
		t.Fatalf("rendering a page ran %d tag queries, want 1 for the whole page", n)
	}
}

func storedID(t *testing.T, app core.App, collection, pbID string) int {
	t.Helper()
	record, err := app.FindRecordById(collection, pbID)
	if err != nil {
		t.Fatalf("load %s %s: %v", collection, pbID, err)
	}
	return ngxIDOf(record)
}
