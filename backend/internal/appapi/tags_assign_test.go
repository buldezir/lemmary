package appapi

import (
	"context"
	"slices"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// candidateDB mirrors countDB, plus the two columns the candidate query reads:
// processing_status and created. d5 carries the legacy empty-string tags value
// json_each would otherwise abort the whole query on.
func candidateDB(t *testing.T) dbx.Builder {
	t.Helper()
	db, err := dbx.Open("sqlite", "file::memory:?cache=shared&_pragma=foreign_keys(0)")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.NewQuery(`CREATE TABLE documents (
		id TEXT PRIMARY KEY, user TEXT, tags TEXT, ocr_text TEXT,
		processing_status TEXT, created TEXT)`).Execute()
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	rows := []struct {
		id, user, tags, ocr, status, created string
	}{
		{"has-it", "me", `["tax","paid"]`, "text", "completed", "2025-01-01"},
		{"wants-it", "me", `["paid"]`, "text", "completed", "2025-01-02"},
		{"untagged", "me", `[]`, "text", "needs_review", "2025-01-03"},
		{"legacy", "me", ``, "text", "completed", "2025-01-04"},
		{"no-text", "me", `[]`, "", "completed", "2025-01-05"},
		{"queued", "me", `[]`, "text", "pending", "2025-01-06"},
		{"running", "me", `[]`, "text", "processing", "2025-01-07"},
		{"theirs", "you", `[]`, "text", "completed", "2025-01-08"},
	}
	for _, r := range rows {
		_, err := db.NewQuery(`INSERT INTO documents (id, user, tags, ocr_text, processing_status, created)
			VALUES ({:id}, {:user}, {:tags}, {:ocr}, {:status}, {:created})`).Bind(dbx.Params{
			"id": r.id, "user": r.user, "tags": r.tags, "ocr": r.ocr, "status": r.status, "created": r.created,
		}).Execute()
		if err != nil {
			t.Fatalf("insert %s: %v", r.id, err)
		}
	}
	return db
}

func TestTagAssignCandidatesSkipWhatCannotOrNeedNotBeAsked(t *testing.T) {
	db := candidateDB(t)

	ids, err := tagAssignCandidateIDs(db, "me", []string{"tax"}, maxTagAssignDocuments)
	if err != nil {
		t.Fatalf("candidate ids: %v", err)
	}
	want := []string{"legacy", "untagged", "wants-it"}
	got := slices.Clone(ids)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}

	total, err := countTagAssignCandidates(db, "me", []string{"tax"})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != len(want) {
		t.Fatalf("count = %d, want %d", total, len(want))
	}
}

func TestTagAssignCandidatesNewestFirst(t *testing.T) {
	db := candidateDB(t)

	ids, err := tagAssignCandidateIDs(db, "me", []string{"tax"}, 1)
	if err != nil {
		t.Fatalf("candidate ids: %v", err)
	}
	if len(ids) != 1 || ids[0] != "legacy" {
		t.Fatalf("limited candidates = %v, want [legacy]", ids)
	}
}

// Several tags is one pass, not one per tag: a document is a candidate while it
// is missing any of them, so "has-it" still qualifies on the tag it lacks.
func TestTagAssignCandidatesCoverEveryTagInOnePass(t *testing.T) {
	db := candidateDB(t)

	ids, err := tagAssignCandidateIDs(db, "me", []string{"tax", "paid"}, maxTagAssignDocuments)
	if err != nil {
		t.Fatalf("candidate ids: %v", err)
	}
	got := slices.Clone(ids)
	slices.Sort(got)
	// Only "has-it" carries both; everything else is missing at least one, and
	// the excluded rows are excluded for the reasons they always were.
	want := []string{"legacy", "untagged", "wants-it"}
	if !slices.Equal(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}

	total, err := countTagAssignCandidates(db, "me", []string{"tax", "paid"})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != len(want) {
		t.Fatalf("count = %d, want %d", total, len(want))
	}
}

func makeTag(t *testing.T, app core.App, ownerID, name string) *core.Record {
	t.Helper()
	tags, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("tags collection: %v", err)
	}
	tag := core.NewRecord(tags)
	tag.Set("user", ownerID)
	tag.Set("name", name)
	if err := app.Save(tag); err != nil {
		t.Fatalf("save tag %q: %v", name, err)
	}
	return tag
}

// The test that pins the whole requirement: the tag is merged in and every
// other field the extractor would have rewritten is byte-identical afterwards.
func TestRunTagAssignMergesAndTouchesNothingElse(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "assign-merge@example.test")

	invoices := makeTag(t, app, owner, "Invoices")
	keepMe := makeTag(t, app, owner, "Keep me")

	document := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "an invoice from acme")
	document.Set("tags", []string{keepMe.Id})
	document.Set("title", "Hand-written title")
	document.Set("confidence", 0.42)
	document.Set("metadata_source", "user")
	document.Set("document_date", "2025-04-01")
	if err := app.Save(document); err != nil {
		t.Fatalf("seed document: %v", err)
	}

	helper := &fakeHelper{values: map[string]map[string]string{
		// Lower-cased and punctuated on purpose: the matcher normalizes.
		document.Id: {"tags": " invoices "},
	}}
	result, err := runTagAssign(context.Background(), app, helper, owner,
		[]string{invoices.Id}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Assigned != 1 || result.Declined != 0 || result.Failed != 0 {
		t.Fatalf("result = %+v, want one assigned", result)
	}

	after, err := app.FindRecordById("documents", document.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	tags := after.GetStringSlice("tags")
	slices.Sort(tags)
	want := []string{invoices.Id, keepMe.Id}
	slices.Sort(want)
	if !slices.Equal(tags, want) {
		t.Fatalf("tags = %v, want %v", tags, want)
	}
	if got := after.GetString("title"); got != "Hand-written title" {
		t.Fatalf("title = %q, want it untouched", got)
	}
	if got := after.GetString("metadata_source"); got != "user" {
		t.Fatalf("metadata_source = %q, want it untouched", got)
	}
	if got := after.GetString("processing_status"); got != models.DocStatusCompleted {
		t.Fatalf("processing_status = %q, want it untouched", got)
	}
	if got := after.GetFloat("confidence"); got != 0.42 {
		t.Fatalf("confidence = %v, want it untouched", got)
	}
	if got := after.GetString("document_date"); got == "" {
		t.Fatal("document_date was cleared")
	}
}

func TestRunTagAssignWritesNothingWhenTheModelDeclines(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "assign-decline@example.test")
	invoices := makeTag(t, app, owner, "Invoices")
	document := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "a postcard")

	before := document.GetDateTime("updated").String()

	helper := &fakeHelper{values: map[string]map[string]string{document.Id: {"tags": ""}}}
	result, err := runTagAssign(context.Background(), app, helper, owner,
		[]string{invoices.Id}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Assigned != 0 || result.Declined != 1 {
		t.Fatalf("result = %+v, want one decline", result)
	}

	after, err := app.FindRecordById("documents", document.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(after.GetStringSlice("tags")) != 0 {
		t.Fatalf("tags = %v, want none", after.GetStringSlice("tags"))
	}
	if got := after.GetDateTime("updated").String(); got != before {
		t.Fatalf("updated moved from %q to %q on a decline", before, got)
	}
}

// The multi-tag run's whole point: one pass writes every tag the model named.
func TestRunTagAssignMergesEveryNamedTagInOnePass(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "assign-multi@example.test")
	invoices := makeTag(t, app, owner, "Invoices")
	tax := makeTag(t, app, owner, "Tax")
	unnamed := makeTag(t, app, owner, "Holiday")

	document := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "an invoice for tax")

	helper := &fakeHelper{values: map[string]map[string]string{
		document.Id: {"tags": "Invoices, Tax"},
	}}
	result, err := runTagAssign(context.Background(), app, helper, owner,
		[]string{invoices.Id, tax.Id, unnamed.Id}, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Assigned != 1 {
		t.Fatalf("result = %+v, want one document assigned", result)
	}
	// One pass, not three: the helper saw the document once.
	if len(helper.batches) != 1 {
		t.Fatalf("helper called %d times, want 1", len(helper.batches))
	}

	after, err := app.FindRecordById("documents", document.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	got := after.GetStringSlice("tags")
	slices.Sort(got)
	want := []string{invoices.Id, tax.Id}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("tags = %v, want %v -- the tag it did not name must stay off", got, want)
	}
}

// Only what was offered: a name outside the vocabulary is one the model
// invented, even when a tag by that name exists.
func TestRunTagAssignKeepsToTheOfferedVocabulary(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "assign-vocab@example.test")
	invoices := makeTag(t, app, owner, "Invoices")
	makeTag(t, app, owner, "Tax")

	document := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "an invoice")

	helper := &fakeHelper{values: map[string]map[string]string{
		document.Id: {"tags": "Invoices, Tax, Groceries"},
	}}
	if _, err := runTagAssign(context.Background(), app, helper, owner,
		[]string{invoices.Id}, nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	after, err := app.FindRecordById("documents", document.Id)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := after.GetStringSlice("tags"); !slices.Equal(got, []string{invoices.Id}) {
		t.Fatalf("tags = %v, want only the offered tag", got)
	}
}
