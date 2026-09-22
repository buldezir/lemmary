package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds the consume-folder settings: who owns what the folder yields, how often
// it is scanned, and whether the original file is removed afterwards. Also the
// ingest_files ledger, so a file kept in the folder is not imported again after
// its document was deleted or the process restarted.
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
	if changed {
		if err := app.Save(settings); err != nil {
			return err
		}
	}
	if _, err := app.FindCollectionByNameOrId(ingestFilesCollection); err == nil {
		return nil
	}
	ledger := core.NewBaseCollection(ingestFilesCollection)
	ledger.Fields.Add(
		&core.RelationField{
			Name:          "user",
			Required:      true,
			CollectionId:  "_pb_users_auth_",
			MaxSelect:     1,
			CascadeDelete: true,
		},
		&core.TextField{Name: "path", Required: true, Max: 4096},
		&core.TextField{Name: "stamp", Max: 100},
	)
	ledger.AddIndex("idx_ingest_files_user_path", true, "user, path", "")
	return app.Save(ledger)
}

const ingestFilesCollection = "ingest_files"

func dropIngestDirFields(app core.App) error {
	if ledger, err := app.FindCollectionByNameOrId(ingestFilesCollection); err == nil {
		if err := app.Delete(ledger); err != nil {
			return err
		}
	}
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	for _, field := range ingestDirFields() {
		settings.Fields.RemoveByName(field.GetName())
	}
	return app.Save(settings)
}
