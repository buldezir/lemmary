package fulltext

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	_ "lemmary/backend/migrations"

	"lemmary/backend/internal/testpb"
)

// The whole sharing story in search rests on one term filter matching any value
// of a multi-valued field. If that stops holding, every search surface breaks at
// once and nothing else in this package would notice.
func TestSearchFindsADocumentForEveryIndexedReader(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "shared", map[string]any{
		FieldUser:    []string{"owner", "recipient"},
		FieldAll:     "quarterly invoice",
		FieldOCRText: "quarterly invoice",
	})
	mustPut(t, idx, "private", map[string]any{
		FieldUser:    []string{"owner"},
		FieldAll:     "quarterly invoice",
		FieldOCRText: "quarterly invoice",
	})

	for _, user := range []string{"owner", "recipient"} {
		ids := searchIDs(t, idx, Query{Text: "invoice", UserID: user})
		if !containsID(ids, "shared") {
			t.Fatalf("%s cannot find the shared document: %v", user, ids)
		}
	}
	if ids := searchIDs(t, idx, Query{Text: "invoice", UserID: "recipient"}); containsID(ids, "private") {
		t.Fatalf("recipient found an unshared document: %v", ids)
	}
	if ids := searchIDs(t, idx, Query{Text: "invoice", UserID: "stranger"}); len(ids) != 0 {
		t.Fatalf("a stranger found documents: %v", ids)
	}
}

// The "shared" marker is a filter, not a tag record, so the index is what has
// to tell a borrowed document from an owned one.
func TestSharedOnlyKeepsWhatSomebodyElseOwns(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "shared", map[string]any{
		FieldUser:    []string{"owner", "recipient"},
		FieldOwner:   "owner",
		FieldAll:     "quarterly invoice",
		FieldOCRText: "quarterly invoice",
	})
	mustPut(t, idx, "mine", map[string]any{
		FieldUser:    []string{"recipient"},
		FieldOwner:   "recipient",
		FieldAll:     "quarterly invoice",
		FieldOCRText: "quarterly invoice",
	})

	ids := searchIDs(t, idx, Query{Text: "invoice", UserID: "recipient", SharedOnly: true})
	if len(ids) != 1 || ids[0] != "shared" {
		t.Fatalf("shared-only search returned %v, want [shared]", ids)
	}
	if ids := searchIDs(t, idx, Query{Text: "invoice", UserID: "recipient"}); len(ids) != 2 {
		t.Fatalf("unfiltered search returned %v, want both", ids)
	}
	if ids := searchIDs(t, idx, Query{Text: "invoice", UserID: "owner", SharedOnly: true}); len(ids) != 0 {
		t.Fatalf("the owner found their own document as shared: %v", ids)
	}
}

func TestReadersOfIsOwnerPlusRecipients(t *testing.T) {
	app := testpb.Open(t)
	owner := shareTestUser(t, app, "owner@example.com")
	first := shareTestUser(t, app, "first@example.com")
	second := shareTestUser(t, app, "second@example.com")
	shared := shareTestDocument(t, app, owner)
	private := shareTestDocument(t, app, owner)
	shareTestGrant(t, app, shared, first)
	shareTestGrant(t, app, shared, second)

	// Both paths have to agree: one query per document, and one query for all
	// of them on a rebuild.
	preloaded := newNameCache(app)
	preloaded.preloadReaders()
	for name, cache := range map[string]*nameCache{"perDocument": newNameCache(app), "preloaded": preloaded} {
		got := cache.readersOf(mustLoad(t, app, shared))
		if len(got) != 3 || got[0] != owner {
			t.Fatalf("%s: readers of a shared document = %v", name, got)
		}
		if !containsID(got, first) || !containsID(got, second) {
			t.Fatalf("%s: readers of a shared document = %v", name, got)
		}
		if got := cache.readersOf(mustLoad(t, app, private)); len(got) != 1 || got[0] != owner {
			t.Fatalf("%s: readers of an unshared document = %v", name, got)
		}
	}
}

func mustLoad(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()
	rec, err := app.FindRecordById("documents", id)
	if err != nil {
		t.Fatalf("load document %s: %v", id, err)
	}
	return rec
}

func shareTestUser(t *testing.T, app core.App, email string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	rec := core.NewRecord(collection)
	rec.Set("email", email)
	rec.SetPassword("test-password-123")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return rec.Id
}

func shareTestDocument(t *testing.T, app core.App, userID string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	rec := core.NewRecord(collection)
	rec.Set("user", userID)
	rec.Set("title", "invoice")
	file, err := filesystem.NewFileFromBytes([]byte("invoice"), "invoice.txt")
	if err != nil {
		t.Fatalf("build file: %v", err)
	}
	rec.Set("file", file)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save document: %v", err)
	}
	return rec.Id
}

func shareTestGrant(t *testing.T, app core.App, documentID, userID string) {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId(CollectionShares)
	if err != nil {
		t.Fatalf("document_shares collection: %v", err)
	}
	rec := core.NewRecord(collection)
	rec.Set("document", documentID)
	rec.Set("user", userID)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save share: %v", err)
	}
}
