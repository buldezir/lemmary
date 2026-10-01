package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// Down then up on a migrated app: the round trip a managed instance's re-run
// and a rollback both take. Ids, owners, ngx ids and the documents' links must
// all come through.
func TestNamedEntitiesMoveIntoOptions(t *testing.T) {
	app := bootMigratedApp(t)
	if err := moveOptionsToNamedEntities(app); err != nil {
		t.Fatalf("down: %v", err)
	}
	userID := makeUser(t, app, "owner@example.com")
	docID := makeDocument(t, app, userID, "Invoice")

	saveNamed := func(collection, name string, ngxID int) string {
		t.Helper()
		coll, err := app.FindCollectionByNameOrId(collection)
		if err != nil {
			t.Fatalf("%s after down: %v", collection, err)
		}
		record := core.NewRecord(coll)
		record.Set("user", userID)
		record.Set("name", name)
		record.Set("ngx_id", ngxID)
		if err := app.Save(record); err != nil {
			t.Fatalf("save %s: %v", collection, err)
		}
		return record.Id
	}
	// The same ngx id in both: they were separate number spaces.
	corrID := saveNamed("correspondents", "Acme", 7)
	typeID := saveNamed("document_types", "Invoice", 7)
	doc, err := app.FindRecordById("documents", docID)
	if err != nil {
		t.Fatal(err)
	}
	doc.Set("correspondent", corrID)
	doc.Set("document_type", typeID)
	if err := app.Save(doc); err != nil {
		t.Fatalf("link document: %v", err)
	}

	for range 2 {
		if err := moveNamedEntitiesToOptions(app); err != nil {
			t.Fatalf("up: %v", err)
		}
	}

	for _, name := range []string{"correspondents", "document_types"} {
		if _, err := app.FindCollectionByNameOrId(name); err == nil {
			t.Fatalf("%s survived the move", name)
		}
	}
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	if documents.Fields.GetByName("correspondent") != nil || documents.Fields.GetByName("document_type") != nil {
		t.Fatal("the relation columns survived the move")
	}
	for id, fieldID := range map[string]string{corrID: models.CorrespondentFieldID, typeID: models.DocumentTypeFieldID} {
		option, err := app.FindRecordById(models.CustomFieldOptionsCollection, id)
		if err != nil {
			t.Fatalf("option %s: %v", id, err)
		}
		if option.GetString("field") != fieldID || option.GetString("user") != userID || option.GetInt("ngx_id") != 7 {
			t.Fatalf("option %s = %v", id, option.PublicExport())
		}
	}
	values, err := models.LoadDocumentFieldValues(app, docID)
	if err != nil {
		t.Fatal(err)
	}
	if values.OptionName(models.CorrespondentFieldID) != "Acme" || values.OptionName(models.DocumentTypeFieldID) != "Invoice" {
		t.Fatalf("values = %#v", values)
	}
}
