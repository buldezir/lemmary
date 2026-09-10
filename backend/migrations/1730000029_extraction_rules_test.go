package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestExtractionRulesCanBeSavedAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	// The singleton is seeded at boot by config.EnsureDefaults, not by a
	// migration, so the test writes its own.
	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	settings.Set("extraction_rules", "Treat Rechnung as the document type Invoice.")
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}

	reloaded, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil {
		t.Fatalf("reload app_settings: %v", err)
	}
	if got := reloaded.GetString("extraction_rules"); got != "Treat Rechnung as the document type Invoice." {
		t.Fatalf("extraction_rules did not round-trip through the record, got %q", got)
	}
}

// A managed instance re-runs every migration on every boot, so the field has to
// be safe to add twice.
func TestExtractionRulesMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	if err := addExtractionRulesField(app); err != nil {
		t.Fatalf("addExtractionRulesField on an already-migrated app: %v", err)
	}
	if err := addExtractionRulesField(app); err != nil {
		t.Fatalf("addExtractionRulesField a second time: %v", err)
	}
}
