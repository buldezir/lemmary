package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
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
	otherTagID := makeNamedEntity(t, app, "tags", owner, "unused")

	docID := makeDocument(t, app, owner, "invoice")
	doc, err := app.FindRecordById("documents", docID)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	doc.Set("tags", []string{tagID})
	if err := app.Save(doc); err != nil {
		t.Fatalf("attach tags: %v", err)
	}
	optionID := makeOption(t, app, models.CorrespondentFieldID, owner, "acme")
	if err := models.SaveFieldValue(app, doc, models.CorrespondentField, optionID); err != nil {
		t.Fatalf("attach correspondent: %v", err)
	}
	values, err := app.FindRecordsByFilter(models.CustomFieldValuesCollection, "document = {:d}", "", 0, 0,
		map[string]any{"d": docID})
	if err != nil || len(values) != 1 {
		t.Fatalf("value rows: %v, %v", values, err)
	}
	makeShare(t, app, docID, other)

	info := asUser(t, app, other)
	for _, entity := range []struct{ collection, id string }{
		{"tags", tagID},
		{models.CustomFieldOptionsCollection, optionID},
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
	valuesColl, err := app.FindCollectionByNameOrId(models.CustomFieldValuesCollection)
	if err != nil {
		t.Fatal(err)
	}
	if !canAccess(t, app, models.CustomFieldValuesCollection, values[0].Id, info, valuesColl.ViewRule) {
		t.Fatal("a value on a shared document is not readable by the recipient")
	}
}

func makeOption(t *testing.T, app core.App, fieldID, userID, name string) string {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldOptionsCollection)
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(coll)
	record.Set("field", fieldID)
	record.Set("user", userID)
	record.Set("name", name)
	if err := app.Save(record); err != nil {
		t.Fatalf("save option %s: %v", name, err)
	}
	return record.Id
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
	// The collections this migration ruled over until 1730000051.
	if err := moveOptionsToNamedEntities(app); err != nil {
		t.Fatalf("roll back 1730000051: %v", err)
	}
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

func TestLibraryShareCoversFutureDocumentsAndPreservesExplicitShares(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "library-owner@example.com")
	reader := makeUser(t, app, "library-reader@example.com")
	stranger := makeUser(t, app, "library-stranger@example.com")
	existing := makeDocument(t, app, owner, "existing")
	explicit := makeDocument(t, app, owner, "explicit")
	makeShare(t, app, explicit, reader)
	coll, err := app.FindCollectionByNameOrId(librarySharesCollection)
	if err != nil {
		t.Fatal(err)
	}
	grant := core.NewRecord(coll)
	grant.Set("owner", owner)
	grant.Set("recipient", reader)
	if err := app.Save(grant); err != nil {
		t.Fatal(err)
	}
	future := makeDocument(t, app, owner, "future")
	docs := documentRules(t, app)
	for _, id := range []string{existing, explicit, future} {
		if !canAccess(t, app, "documents", id, asUser(t, app, reader), docs.ViewRule) {
			t.Fatalf("reader cannot read %s", id)
		}
		for _, info := range []*core.RequestInfo{asUser(t, app, stranger), {}} {
			if canAccess(t, app, "documents", id, info, docs.ViewRule) {
				t.Fatal("unrelated caller can read library")
			}
		}
		if canAccess(t, app, "documents", id, asUser(t, app, reader), docs.UpdateRule) {
			t.Fatal("reader can edit library")
		}
	}
	if !canAccess(t, app, librarySharesCollection, grant.Id, asUser(t, app, owner), coll.DeleteRule) {
		t.Fatal("owner cannot revoke")
	}
	if canAccess(t, app, librarySharesCollection, grant.Id, asUser(t, app, reader), coll.DeleteRule) {
		t.Fatal("reader can revoke")
	}
	if err := app.Save(core.NewRecord(coll)); err == nil {
		t.Fatal("missing relations accepted")
	}
	duplicate := core.NewRecord(coll)
	duplicate.Set("owner", owner)
	duplicate.Set("recipient", reader)
	if err := app.Save(duplicate); err == nil {
		t.Fatal("duplicate grant accepted")
	}
	if err := app.Delete(grant); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{existing, future} {
		if canAccess(t, app, "documents", id, asUser(t, app, reader), docs.ViewRule) {
			t.Fatal("revoked library still readable")
		}
	}
	if !canAccess(t, app, "documents", explicit, asUser(t, app, reader), docs.ViewRule) {
		t.Fatal("explicit grant lost")
	}
}

func TestLibraryGrantDoesNotReshareAndFollowsOwnership(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "library-owner@example.com")
	reader := makeUser(t, app, "library-reader@example.com")
	third := makeUser(t, app, "library-third@example.com")
	borrowed := makeDocument(t, app, third, "borrowed")
	makeShare(t, app, borrowed, owner)
	own := makeDocument(t, app, owner, "owned")
	coll, err := app.FindCollectionByNameOrId(librarySharesCollection)
	if err != nil {
		t.Fatal(err)
	}
	grant := core.NewRecord(coll)
	grant.Set("owner", owner)
	grant.Set("recipient", reader)
	if err := app.Save(grant); err != nil {
		t.Fatal(err)
	}
	docs := documentRules(t, app)
	if canAccess(t, app, "documents", borrowed, asUser(t, app, reader), docs.ViewRule) {
		t.Fatal("borrowed document reshared")
	}
	record, err := app.FindRecordById("documents", own)
	if err != nil {
		t.Fatal(err)
	}
	record.Set("user", third)
	if err := app.Save(record); err != nil {
		t.Fatal(err)
	}
	if canAccess(t, app, "documents", own, asUser(t, app, reader), docs.ViewRule) {
		t.Fatal("library access survived ownership transfer")
	}
	account, err := app.FindRecordById("users", reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(account); err != nil {
		t.Fatal(err)
	}
	if _, err := app.FindRecordById(librarySharesCollection, grant.Id); err == nil {
		t.Fatal("deleted recipient left a grant")
	}
}
