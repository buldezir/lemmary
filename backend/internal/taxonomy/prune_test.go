package taxonomy_test

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/taxonomy"
	// Blank import on purpose: RunAppMigrations only runs what this package
	// registered, and the taxonomy package itself never imports it.
	_ "lemmary/backend/migrations"
)

func bootTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	if err := app.RunAppMigrations(); err != nil {
		t.Fatalf("run app migrations: %v", err)
	}
	return app
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

func createNamed(t *testing.T, app core.App, collection, name, userID string) string {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatalf("%s collection: %v", collection, err)
	}
	record := core.NewRecord(coll)
	record.Set("name", name)
	record.Set("name_original", name)
	record.Set("user", userID)
	if err := app.Save(record); err != nil {
		t.Fatalf("save %s %q: %v", collection, name, err)
	}
	return record.Id
}

// An unused tag is the normal state of a tag its owner just created.
func TestPruneOrphansKeepsUnusedTags(t *testing.T) {
	app := bootTestApp(t)
	user := createUser(t, app, "owner@example.com")

	tag := createNamed(t, app, "tags", "Invoices", user)
	correspondent := createNamed(t, app, "correspondents", "Acme GmbH", user)
	documentType := createNamed(t, app, "document_types", "Invoice", user)

	result, err := taxonomy.PruneOrphans(app)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if result.Tags != 0 {
		t.Fatalf("prune removed %d tags; tags are never pruned", result.Tags)
	}
	if _, err := app.FindRecordById("tags", tag); err != nil {
		t.Fatalf("unused tag was deleted: %v", err)
	}

	if result.Correspondents != 1 || result.DocumentTypes != 1 {
		t.Fatalf("expected 1 correspondent and 1 document type removed, got %+v", result)
	}
	if _, err := app.FindRecordById("correspondents", correspondent); err == nil {
		t.Fatal("expected the orphan correspondent to be deleted")
	}
	if _, err := app.FindRecordById("document_types", documentType); err == nil {
		t.Fatal("expected the orphan document type to be deleted")
	}
}
