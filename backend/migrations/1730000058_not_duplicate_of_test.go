package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestNotDuplicateOfReturnsReviewedDuplicatesToTheInbox(t *testing.T) {
	app := bootMigratedApp(t)
	userID := makeUser(t, app, "owner@example.com")
	originalID := makeDocument(t, app, userID, "invoice")
	keptID := makeDocument(t, app, userID, "invoice copy")
	otherID := makeDocument(t, app, userID, "receipt")
	if _, err := app.DB().NewQuery(
		"UPDATE documents SET processing_status = 'completed', duplicate_of = (CASE WHEN id = {:kept} THEN {:original} ELSE '' END)",
	).Bind(map[string]any{"kept": keptID, "original": originalID}).Execute(); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		if err := addNotDuplicateOf(app); err != nil {
			t.Fatalf("add on a migrated app: %v", err)
		}
	}

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	field, ok := documents.Fields.GetByName("not_duplicate_of").(*core.RelationField)
	if !ok || field.CollectionId != documents.Id || !field.IsMultiple() || !field.Hidden || field.CascadeDelete {
		t.Fatalf("documents.not_duplicate_of = %#v, want a hidden non-cascading multi-relation to documents", field)
	}
	for id, want := range map[string]string{keptID: "needs_review", originalID: "completed", otherID: "completed"} {
		record, err := app.FindRecordById("documents", id)
		if err != nil {
			t.Fatal(err)
		}
		if got := record.GetString("processing_status"); got != want {
			t.Fatalf("%s status = %q, want %q", record.GetString("title"), got, want)
		}
	}

	if err := dropNotDuplicateOf(app); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if err := addNotDuplicateOf(app); err != nil {
		t.Fatalf("re-add: %v", err)
	}
}
