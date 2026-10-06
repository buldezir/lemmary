package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestRelatedDocumentsAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)
	for range 2 {
		if err := addRelatedDocuments(app); err != nil {
			t.Fatalf("add on a migrated app: %v", err)
		}
	}

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	related, ok := documents.Fields.GetByName("related").(*core.RelationField)
	if !ok || related.CollectionId != documents.Id || !related.IsMultiple() || related.CascadeDelete {
		t.Fatalf("documents.related = %#v, want a non-cascading multi-relation to documents", related)
	}
	if field, ok := documents.Fields.GetByName("reference_numbers").(*core.JSONField); !ok || !field.Hidden {
		t.Fatal("documents.reference_numbers must be a hidden JSON field")
	}

	for range 2 {
		if err := dropRelatedDocuments(app); err != nil {
			t.Fatalf("drop: %v", err)
		}
	}
	if err := addRelatedDocuments(app); err != nil {
		t.Fatalf("re-add: %v", err)
	}
}

func TestDeletingARelatedDocumentKeepsTheOneLinkingToIt(t *testing.T) {
	app := bootMigratedApp(t)
	userID := makeUser(t, app, "owner@example.com")
	linking, err := app.FindRecordById("documents", makeDocument(t, app, userID, "invoice"))
	if err != nil {
		t.Fatal(err)
	}
	linkedID := makeDocument(t, app, userID, "receipt")
	linking.Set("related", []string{linkedID})
	if err := app.Save(linking); err != nil {
		t.Fatalf("link: %v", err)
	}

	linked, err := app.FindRecordById("documents", linkedID)
	if err != nil {
		t.Fatal(err)
	}
	if err := app.Delete(linked); err != nil {
		t.Fatalf("delete the linked document: %v", err)
	}

	linking, err = app.FindRecordById("documents", linking.Id)
	if err != nil {
		t.Fatalf("the linking document went with it: %v", err)
	}
	if got := linking.GetStringSlice("related"); len(got) != 0 {
		t.Fatalf("related = %v after its target was deleted, want empty", got)
	}
}
