package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds the IMAP ingest settings: the mailbox to read, and what happens to a
// message once its attachments are documents. Keep mode records its progress
// in the ingest_files ledger 1730000042 created.
func init() {
	m.Register(addIngestIMAPFields, dropIngestIMAPFields)
}

func ingestIMAPFields() []core.Field {
	return []core.Field{
		&core.TextField{Name: "imap_host", Max: 500},
		&core.TextField{Name: "imap_security", Max: 20},
		&core.TextField{Name: "imap_username", Max: 500},
		&core.TextField{Name: "imap_password", Max: 2000},
		&core.TextField{Name: "imap_folder", Max: 500},
		&core.TextField{Name: "imap_after_consume", Max: 20},
		&core.TextField{Name: "imap_move_folder", Max: 500},
	}
}

func addIngestIMAPFields(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	changed := false
	for _, field := range ingestIMAPFields() {
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

func dropIngestIMAPFields(app core.App) error {
	settings, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	for _, field := range ingestIMAPFields() {
		settings.Fields.RemoveByName(field.GetName())
	}
	return app.Save(settings)
}
