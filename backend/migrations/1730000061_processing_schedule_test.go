package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

func TestProcessingScheduleReachesConfig(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	settings.Set("processing_window_enabled", true)
	settings.Set("processing_window_start", "22:00")
	settings.Set("processing_window_end", "07:00")
	settings.Set("processing_timezone", "Europe/Berlin")
	settings.Set("processing_ping_enabled", true)
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}

	cfg, err := config.Load(app)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	want := config.ProcessingWindow{Enabled: true, Start: "22:00", End: "07:00", Timezone: "Europe/Berlin"}
	if cfg.ProcessingWindow != want || !cfg.ProcessingPing {
		t.Fatalf("Config window = %+v, ping = %v", cfg.ProcessingWindow, cfg.ProcessingPing)
	}
}

func TestProcessingScheduleMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	if err := addProcessingScheduleFields(app); err != nil {
		t.Fatalf("addProcessingScheduleFields on an already-migrated app: %v", err)
	}
	if err := addProcessingScheduleFields(app); err != nil {
		t.Fatalf("addProcessingScheduleFields a second time: %v", err)
	}
}
