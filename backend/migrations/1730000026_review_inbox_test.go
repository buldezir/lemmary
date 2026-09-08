package migrations

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestAlwaysRequireReviewCanBeSavedAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	// The singleton is seeded at boot by config.EnsureDefaults, not by a
	// migration, so the test writes its own -- what is being checked is that
	// the column the migration added accepts and returns a value.
	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	settings.Set("always_require_review", true)
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}

	reloaded, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil {
		t.Fatalf("reload app_settings: %v", err)
	}
	if !reloaded.GetBool("always_require_review") {
		t.Fatal("always_require_review did not round-trip through the record")
	}
}

func TestTheDocumentStatusIndexExistsAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}

	found := false
	for _, index := range documents.Indexes {
		if strings.Contains(index, "idx_documents_user_processing_status") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("idx_documents_user_processing_status is missing; indexes: %v", documents.Indexes)
	}
}

// A managed instance re-runs every migration on every boot, so both halves have
// to be safe to apply twice. The second run is where a non-idempotent field add
// or index add would fail.
func TestReviewInboxMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	for _, step := range []struct {
		name string
		run  func(core.App) error
	}{
		{"addAlwaysRequireReviewField", addAlwaysRequireReviewField},
		{"addDocumentStatusIndex", addDocumentStatusIndex},
	} {
		if err := step.run(app); err != nil {
			t.Fatalf("%s on an already-migrated app: %v", step.name, err)
		}
		if err := step.run(app); err != nil {
			t.Fatalf("%s a second time: %v", step.name, err)
		}
	}
}
