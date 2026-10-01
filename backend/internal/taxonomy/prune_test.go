package taxonomy_test

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/taxonomy"
	"lemmary/backend/internal/testpb"
	// Blank import on purpose: the shared schema template only includes what
	// this package registered, and the taxonomy package itself never imports it.
	_ "lemmary/backend/migrations"
)

func bootTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	return testpb.Open(t)
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
		t.Fatalf("save user: %v", err)
	}
	return record.Id
}

func createNamed(t *testing.T, app core.App, collection, name, userID string, fields ...string) *core.Record {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("%s collection: %v", collection, err)
	}
	record := core.NewRecord(coll)
	record.Set("name", name)
	record.Set("user", userID)
	for _, field := range fields {
		record.Set("field", field)
	}
	if err := app.Save(record); err != nil {
		t.Fatalf("save %s %q: %v", collection, name, err)
	}
	return record
}

// An unused tag is the normal state of a tag its owner just created; an
// option nothing carries is debris an extraction left behind.
func TestPruneOrphansRemovesOnlyUnusedOptions(t *testing.T) {
	app := bootTestApp(t)
	user := createUser(t, app, "owner@example.com")

	tag := createNamed(t, app, "tags", "Invoices", user)
	correspondent := createNamed(t, app, models.CustomFieldOptionsCollection, "Acme GmbH", user, models.CorrespondentFieldID)
	documentType := createNamed(t, app, models.CustomFieldOptionsCollection, "Invoice", user, models.DocumentTypeFieldID)
	used := createNamed(t, app, models.CustomFieldOptionsCollection, "Receipt", user, models.DocumentTypeFieldID)

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	file, err := filesystem.NewFileFromBytes([]byte("x"), "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc := core.NewRecord(documents)
	doc.Set("user", user)
	doc.Set("file", file)
	if err := app.Save(doc); err != nil {
		t.Fatal(err)
	}
	if err := models.SaveFieldValue(app, doc, models.DocumentTypeField, used.Id); err != nil {
		t.Fatal(err)
	}

	result, err := taxonomy.PruneOrphans(app)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if result.Tags != 0 {
		t.Fatalf("prune removed %d tags; tags are never pruned", result.Tags)
	}
	if _, err := app.FindRecordById("tags", tag.Id); err != nil {
		t.Fatalf("unused tag was deleted: %v", err)
	}
	if result.Correspondents != 1 || result.DocumentTypes != 1 {
		t.Fatalf("expected 1 correspondent and 1 document type removed, got %+v", result)
	}
	for _, orphan := range []*core.Record{correspondent, documentType} {
		if _, err := app.FindRecordById(models.CustomFieldOptionsCollection, orphan.Id); err == nil {
			t.Fatalf("orphan option %q survived", orphan.GetString("name"))
		}
	}
	if _, err := app.FindRecordById(models.CustomFieldOptionsCollection, used.Id); err != nil {
		t.Fatalf("an option a document carries was deleted: %v", err)
	}
}
