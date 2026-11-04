package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/models"
	"testing"
)

func libraryGrant(t *testing.T, app core.App, owner, reader string) *core.Record {
	t.Helper()
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
	return grant
}

func TestLibraryMetadataRequiresAReadableDocument(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "metadata-owner@example.com")
	reader := makeUser(t, app, "metadata-reader@example.com")
	stranger := makeUser(t, app, "metadata-stranger@example.com")
	docID := makeDocument(t, app, owner, "library")
	tag := makeNamedEntity(t, app, "tags", owner, "Used")
	unusedTag := makeNamedEntity(t, app, "tags", owner, "Private")
	usedOption := makeOption(t, app, models.CorrespondentFieldID, owner, "Used")
	unusedOption := makeOption(t, app, models.CorrespondentFieldID, owner, "Private")
	doc, err := app.FindRecordById("documents", docID)
	if err != nil {
		t.Fatal(err)
	}
	doc.Set("tags", []string{tag})
	if err := app.Save(doc); err != nil {
		t.Fatal(err)
	}
	if err := models.SaveFieldValue(app, doc, models.CorrespondentField, usedOption); err != nil {
		t.Fatal(err)
	}
	grant := libraryGrant(t, app, owner, reader)
	for _, name := range []string{"tags", models.CustomFieldOptionsCollection} {
		coll, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			t.Fatal(err)
		}
		coll.ViewRule = new("user = @request.auth.id || (@request.auth.id != '' && user.library_shares_via_owner.recipient ?= @request.auth.id)")
		if err := app.Save(coll); err != nil {
			t.Fatal(err)
		}
	}
	if err := applyLibraryShares(app); err != nil {
		t.Fatal(err)
	}

	for _, entry := range []struct{ collection, used, unused string }{
		{"tags", tag, unusedTag},
		{models.CustomFieldOptionsCollection, usedOption, unusedOption},
	} {
		coll, err := app.FindCollectionByNameOrId(entry.collection)
		if err != nil {
			t.Fatal(err)
		}
		info := asUser(t, app, reader)
		if !canAccess(t, app, entry.collection, entry.used, info, coll.ViewRule) {
			t.Fatal("library metadata not readable")
		}
		if canAccess(t, app, entry.collection, entry.unused, info, coll.ViewRule) {
			t.Fatal("unused library metadata leaked")
		}
		if canAccess(t, app, entry.collection, entry.used, info, coll.ListRule) {
			t.Fatal("library metadata leaked into own list")
		}
		for _, other := range []*core.RequestInfo{asUser(t, app, stranger), {}} {
			if canAccess(t, app, entry.collection, entry.used, other, coll.ViewRule) {
				t.Fatal("metadata leaked to unrelated caller")
			}
		}
	}
	if err := app.Delete(grant); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ collection, id string }{{"tags", tag}, {models.CustomFieldOptionsCollection, usedOption}} {
		coll, err := app.FindCollectionByNameOrId(entry.collection)
		if err != nil {
			t.Fatal(err)
		}
		if canAccess(t, app, entry.collection, entry.id, asUser(t, app, reader), coll.ViewRule) {
			t.Fatal("revoked metadata still readable")
		}
	}
}

func TestLibrarySharesMigrationCanApplyAndRollbackTwice(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "repeat-owner@example.com")
	reader := makeUser(t, app, "repeat-reader@example.com")
	grant := libraryGrant(t, app, owner, reader)
	snapshot := func() map[string]string {
		rules := map[string]string{}
		for _, name := range []string{"documents", "tags", models.CustomFieldOptionsCollection, models.CustomFieldValuesCollection} {
			coll, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				t.Fatal(err)
			}
			rules[name+"/view"] = *coll.ViewRule
			rules[name+"/list"] = *coll.ListRule
		}
		return rules
	}
	granted := snapshot()
	for range 2 {
		if err := applyLibraryShares(app); err != nil {
			t.Fatal(err)
		}
		for name, rule := range snapshot() {
			if rule != granted[name] {
				t.Fatalf("apply changed %s", name)
			}
		}
		if _, err := app.FindRecordById(librarySharesCollection, grant.Id); err != nil {
			t.Fatal("apply lost grant", err)
		}
	}
	coll, err := app.FindCollectionByNameOrId(librarySharesCollection)
	if err != nil {
		t.Fatal(err)
	}
	if len(coll.Indexes) != 2 || len(coll.Fields.FieldNames()) != 4 {
		t.Fatal("duplicate fields or indexes")
	}
	if err := dropLibraryShares(app); err != nil {
		t.Fatal(err)
	}
	original := snapshot()
	if err := dropLibraryShares(app); err != nil {
		t.Fatal(err)
	}
	for name, rule := range snapshot() {
		if rule != original[name] {
			t.Fatalf("second rollback changed %s", name)
		}
	}
	for range 2 {
		if err := applyLibraryShares(app); err != nil {
			t.Fatal(err)
		}
	}
	for name, rule := range snapshot() {
		if rule != granted[name] {
			t.Fatalf("reapply changed %s", name)
		}
	}
}
