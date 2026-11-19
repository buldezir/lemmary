package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(addOCRPageModeField, func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		settings.Fields.RemoveByName("ocr_page_mode")
		return app.Save(settings)
	})
}

func addOCRPageModeField(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	if settings.Fields.GetByName("ocr_page_mode") == nil {
		settings.Fields.Add(&core.TextField{Name: "ocr_page_mode", Max: 8})
	}
	return app.Save(settings)
}
