package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestIngestDirFieldsRoundTripAndAreIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	if err := addIngestDirFields(app); err != nil {
		t.Fatalf("addIngestDirFields a second time: %v", err)
	}

	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	settings.Set("ingest_dir_owner", "user00000000001")
	settings.Set("ingest_dir_interval_min", 7)
	settings.Set("ingest_dir_delete_original", true)
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}

	reloaded, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil {
		t.Fatalf("reload app_settings: %v", err)
	}
	if reloaded.GetString("ingest_dir_owner") != "user00000000001" ||
		reloaded.GetInt("ingest_dir_interval_min") != 7 ||
		!reloaded.GetBool("ingest_dir_delete_original") {
		t.Fatalf("ingest_dir fields did not round-trip: %v", reloaded.PublicExport())
	}
}
