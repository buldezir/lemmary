package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds the consume-folder settings: who owns what the folder yields, how often
// it is scanned, and whether the original file is removed afterwards.
func init() {
	m.Register(addIngestDirFields, dropIngestDirFields)
}

func ingestDirFields() []core.Field {
	return []core.Field{
		&core.TextField{Name: "ingest_dir_owner", Max: 15},
		&core.NumberField{Name: "ingest_dir_interval_min", OnlyInt: true},
		&core.BoolField{Name: "ingest_dir_delete_original"},
	}
}

func addIngestDirFields(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	changed := false
	for _, field := range ingestDirFields() {
		if settings.Fields.GetByName(field.GetName()) != nil {
			continue
		}
		settings.Fields.Add(field)
		changed = true
	}
	if !changed {
		return nil
	}
	return app.Save(settings)
}

func dropIngestDirFields(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	for _, field := range ingestDirFields() {
		settings.Fields.RemoveByName(field.GetName())
	}
	return app.Save(settings)
}
