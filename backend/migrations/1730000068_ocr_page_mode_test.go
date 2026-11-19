package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

func TestOCRPageModeReachesConfig(t *testing.T) {
	app := bootMigratedApp(t)
	if err := addOCRPageModeField(app); err != nil {
		t.Fatalf("addOCRPageModeField on an already-migrated app: %v", err)
	}

	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("app_settings collection: %v", err)
	}
	settings := core.NewRecord(collection)
	settings.Id = "appsettings0001"
	settings.MarkAsNew()
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}
	cfg, err := config.Load(app)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.OCRPageMode != config.OCRPageModeAuto {
		t.Fatalf("unset OCRPageMode = %q, want auto", cfg.OCRPageMode)
	}

	settings.Set("ocr_page_mode", config.OCRPageModeAlways)
	if err := app.Save(settings); err != nil {
		t.Fatalf("save app_settings: %v", err)
	}
	if cfg, err = config.Load(app); err != nil || cfg.OCRPageMode != config.OCRPageModeAlways {
		t.Fatalf("OCRPageMode = %q (err %v), want always", cfg.OCRPageMode, err)
	}
}
