package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func makeShare(t *testing.T, app core.App, documentID, userID string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId(documentSharesCollection)
	if err != nil {
		t.Fatalf("document_shares collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("document", documentID)
	record.Set("user", userID)
	if err := app.Save(record); err != nil {
		t.Fatalf("save share: %v", err)
	}
	return record.Id
}

func asUser(t *testing.T, app core.App, userID string) *core.RequestInfo {
	t.Helper()
	auth, err := app.FindRecordById("users", userID)
	if err != nil {
		t.Fatalf("load user %s: %v", userID, err)
	}
	return &core.RequestInfo{Auth: auth}
}

func canAccess(t *testing.T, app core.App, collection, recordID string, info *core.RequestInfo, rule *string) bool {
	t.Helper()
	record, err := app.FindRecordById(collection, recordID)
	if err != nil {
		t.Fatalf("load %s %s: %v", collection, recordID, err)
	}
	ok, err := app.CanAccessRecord(record, info, rule)
	if err != nil {
		t.Fatalf("evaluate rule %q: %v", derefRule(rule), err)
	}
	return ok
}

func derefRule(rule *string) string {
	if rule == nil {
		return "<nil>"
	}
	return *rule
}

func documentRules(t *testing.T, app core.App) *core.Collection {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	return coll
}

// The whole feature rests on the back relation resolving to a single join, so
// that both halves of the rule bind to the same share row. Assert the rule as
// PocketBase actually evaluates it rather than assert the rule string.
func TestShareMakesADocumentReadableByTheRecipient(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "owner@example.com")
	other := makeUser(t, app, "other@example.com")
	docID := makeDocument(t, app, owner, "invoice")
	documents := documentRules(t, app)

	if canAccess(t, app, "documents", docID, asUser(t, app, other), documents.ViewRule) {
		t.Fatal("document readable before it was shared")
	}

	makeShare(t, app, docID, other)

	if !canAccess(t, app, "documents", docID, asUser(t, app, other), documents.ViewRule) {
		t.Fatal("shared document is not readable by the recipient")
	}
	if !canAccess(t, app, "documents", docID, asUser(t, app, owner), documents.ViewRule) {
		t.Fatal("owner lost read access to their own document")
	}
}

// A back relation is a LEFT JOIN: with no share rows the joined column is NULL,
// and PocketBase matches NULL against the empty string an anonymous caller's
// @request.auth.id resolves to. Without the signed-in guard this rule handed
// every document -- and every document file -- to callers with no session.
func TestTheShareRuleRefusesAnAnonymousCaller(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "owner@example.com")
	other := makeUser(t, app, "other@example.com")
	docID := makeDocument(t, app, owner, "invoice")
	tagID := makeNamedEntity(t, app, "tags", owner, "receipts")
	doc, err := app.FindRecordById("documents", docID)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	doc.Set("tags", []string{tagID})
	if err := app.Save(doc); err != nil {
		t.Fatalf("attach tag: %v", err)
	}

	anonymous := &core.RequestInfo{}
	for _, shared := range []bool{false, true} {
		if shared {
			makeShare(t, app, docID, other)
		}
		documents := documentRules(t, app)
		if canAccess(t, app, "documents", docID, anonymous, documents.ListRule) {
			t.Fatalf("anonymous list allowed (shared=%v)", shared)
		}
		if canAccess(t, app, "documents", docID, anonymous, documents.ViewRule) {
			t.Fatalf("anonymous view allowed (shared=%v)", shared)
		}
		tags, err := app.FindCollectionByNameOrId("tags")
		if err != nil {
			t.Fatalf("tags collection: %v", err)
		}
		if canAccess(t, app, "tags", tagID, anonymous, tags.ViewRule) {
			t.Fatalf("anonymous tag view allowed (shared=%v)", shared)
		}
	}
}

func TestShareGrantsNothingButReading(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "owner@example.com")
	other := makeUser(t, app, "other@example.com")
	docID := makeDocument(t, app, owner, "invoice")
	makeShare(t, app, docID, other)
	documents := documentRules(t, app)

	info := asUser(t, app, other)
	if canAccess(t, app, "documents", docID, info, documents.UpdateRule) {
		t.Fatal("recipient can update a read-only share")
	}
	if canAccess(t, app, "documents", docID, info, documents.DeleteRule) {
		t.Fatal("recipient can delete a read-only share")
	}
}

// A share is for one document, not for its owner's library.
func TestShareDoesNotLeakTheOwnersOtherDocuments(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "owner@example.com")
	other := makeUser(t, app, "other@example.com")
	shared := makeDocument(t, app, owner, "shared")
	private := makeDocument(t, app, owner, "private")
	makeShare(t, app, shared, other)
	documents := documentRules(t, app)

	info := asUser(t, app, other)
	if !canAccess(t, app, "documents", shared, info, documents.ViewRule) {
		t.Fatal("shared document is not readable")
	}
	if canAccess(t, app, "documents", private, info, documents.ViewRule) {
		t.Fatal("a share on one document exposed another")
	}
}

// Without this the recipient sees a document whose chips are all blank, because
// expand silently drops what the related collection's ViewRule refuses.
func TestSharedDocumentCarriesItsOwnersNamedEntities(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "owner@example.com")
	other := makeUser(t, app, "other@example.com")

	tagID := makeNamedEntity(t, app, "tags", owner, "receipts")
	typeID := makeNamedEntity(t, app, "document_types", owner, "invoice")
	corrID := makeNamedEntity(t, app, "correspondents", owner, "acme")
	otherTagID := makeNamedEntity(t, app, "tags", owner, "unused")

	docID := makeDocument(t, app, owner, "invoice")
	doc, err := app.FindRecordById("documents", docID)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	doc.Set("tags", []string{tagID})
	doc.Set("document_type", typeID)
	doc.Set("correspondent", corrID)
	if err := app.Save(doc); err != nil {
		t.Fatalf("attach named entities: %v", err)
	}
	makeShare(t, app, docID, other)

	info := asUser(t, app, other)
	for _, entity := range []struct{ collection, id string }{
		{"tags", tagID},
		{"document_types", typeID},
		{"correspondents", corrID},
	} {
		coll, err := app.FindCollectionByNameOrId(entity.collection)
		if err != nil {
			t.Fatalf("%s collection: %v", entity.collection, err)
		}
		if !canAccess(t, app, entity.collection, entity.id, info, coll.ViewRule) {
			t.Fatalf("%s on a shared document is not readable by the recipient", entity.collection)
		}
		if canAccess(t, app, entity.collection, entity.id, info, coll.ListRule) {
			t.Fatalf("%s leaked into the recipient's own list", entity.collection)
		}
	}

	tags, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("tags collection: %v", err)
	}
	if canAccess(t, app, "tags", otherTagID, info, tags.ViewRule) {
		t.Fatal("a tag on no shared document is readable by the recipient")
	}
}

func makeNamedEntity(t *testing.T, app core.App, collection, userID, name string) string {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("%s collection: %v", collection, err)
	}
	record := core.NewRecord(coll)
	record.Set("user", userID)
	record.Set("name", name)
	if err := app.Save(record); err != nil {
		t.Fatalf("save %s %s: %v", collection, name, err)
	}
	return record.Id
}

// A managed instance re-runs every migration on every boot, so creating the
// collection and rewriting the rules has to be safe to do twice.
func TestDocumentSharesMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)
	if err := createDocumentShares(app); err != nil {
		t.Fatalf("second createDocumentShares: %v", err)
	}
	if err := setSharedReadRules(app, true); err != nil {
		t.Fatalf("second setSharedReadRules: %v", err)
	}

	shares, err := app.FindCollectionByNameOrId(documentSharesCollection)
	if err != nil {
		t.Fatalf("document_shares collection: %v", err)
	}
	if got := len(shares.Fields.FieldNames()); got != len([]string{"id", "document", "user", "created"}) {
		t.Fatalf("fields duplicated on re-run: %v", shares.Fields.FieldNames())
	}
	if got := len(shares.Indexes); got != 2 {
		t.Fatalf("indexes duplicated on re-run: %v", shares.Indexes)
	}
}

// Deleting either side must take the grant with it; a dangling share would
// widen a rule against a record nobody can see.
func TestSharesGoAwayWithTheirDocumentAndTheirUser(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "owner@example.com")
	other := makeUser(t, app, "other@example.com")

	docID := makeDocument(t, app, owner, "by-document")
	shareID := makeShare(t, app, docID, other)
	doc, err := app.FindRecordById("documents", docID)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	if err := app.Delete(doc); err != nil {
		t.Fatalf("delete document: %v", err)
	}
	if _, err := app.FindRecordById(documentSharesCollection, shareID); err == nil {
		t.Fatal("share outlived its document")
	}

	secondDoc := makeDocument(t, app, owner, "by-user")
	secondShare := makeShare(t, app, secondDoc, other)
	recipient, err := app.FindRecordById("users", other)
	if err != nil {
		t.Fatalf("load recipient: %v", err)
	}
	if err := app.Delete(recipient); err != nil {
		t.Fatalf("delete recipient: %v", err)
	}
	if _, err := app.FindRecordById(documentSharesCollection, secondShare); err == nil {
		t.Fatal("share outlived its recipient")
	}
}
