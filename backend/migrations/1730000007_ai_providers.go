package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"lemmary/backend/internal/aiprovider"
)

func init() {
	m.Register(func(app core.App) error {
		if _, err := aiprovider.EnsureCollection(app); err != nil {
			return err
		}

		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		added := false
		for _, field := range []core.Field{
			&core.TextField{Name: "ocr_provider_id", Max: 15},
			&core.TextField{Name: "ocr_model", Max: 200},
			&core.TextField{Name: "extract_provider_id", Max: 15},
			&core.TextField{Name: "extract_model", Max: 200},
			&core.TextField{Name: "chat_provider_id", Max: 15},
			&core.TextField{Name: "chat_model", Max: 200},
			&core.TextField{Name: "search_provider_id", Max: 15},
			&core.TextField{Name: "search_model", Max: 200},
		} {
			if settings.Fields.GetByName(field.GetName()) == nil {
				settings.Fields.Add(field)
				added = true
			}
		}
		if added {
			if err := app.Save(settings); err != nil {
				return err
			}
		}

		record, err := app.FindRecordById("app_settings", "appsettings0001")
		if err != nil {
			return nil
		}
		if err := aiprovider.MigrateLegacySettings(app, record); err != nil {
			return err
		}
		return app.Save(record)
	}, func(app core.App) error {
		if collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName); err == nil {
			if err := app.Delete(collection); err != nil {
				return err
			}
		}
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		for _, name := range []string{
			"ocr_provider_id", "ocr_model",
			"extract_provider_id", "extract_model",
			"chat_provider_id", "chat_model",
			"search_provider_id", "search_model",
		} {
			if f := settings.Fields.GetByName(name); f != nil {
				settings.Fields.RemoveById(f.GetId())
			}
		}
		return app.Save(settings)
	})
}
