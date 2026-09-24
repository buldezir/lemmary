package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestIngestIMAPFieldsRoundTripAndAreIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	if err := addIngestIMAPFields(app); err != nil {
		t.Fatalf("addIngestIMAPFields a second time: %v", err)
	}

	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	settings.Set("imap_host", "imap.example.com:993")
	settings.Set("imap_password", "secret")
	settings.Set("imap_after_consume", "move")
	settings.Set("imap_move_folder", "Lemmary/Done")
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}

	reloaded, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil {
		t.Fatalf("reload app_settings: %v", err)
	}
	if reloaded.GetString("imap_host") != "imap.example.com:993" ||
		reloaded.GetString("imap_password") != "secret" ||
		reloaded.GetString("imap_after_consume") != "move" ||
		reloaded.GetString("imap_move_folder") != "Lemmary/Done" {
		t.Fatalf("imap fields did not round-trip: %v", reloaded.PublicExport())
	}
}
