package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestIMAPSkipTypesRoundTripAndRejectUnknown(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	settings.Set("imap_skip_types", []string{"image", "text"})
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}
	reloaded, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil {
		t.Fatalf("reload app_settings: %v", err)
	}
	if got := reloaded.GetStringSlice("imap_skip_types"); !slices.Equal(got, []string{"image", "text"}) {
		t.Fatalf("imap_skip_types = %v", got)
	}

	reloaded.Set("imap_skip_types", []string{"exe"})
	if err := app.Save(reloaded); err == nil {
		t.Fatal("an unknown file type was stored")
	}
}
