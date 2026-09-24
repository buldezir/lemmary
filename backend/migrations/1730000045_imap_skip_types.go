package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds imap_skip_types: the file types (config.IMAPFileTypes) the mailbox scan
// leaves alone. Empty imports every storable attachment, as before.
func init() {
	m.Register(func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		if settings.Fields.GetByName("imap_skip_types") != nil {
			return nil
		}
		settings.Fields.Add(&core.SelectField{Name: "imap_skip_types", Values: []string{"pdf", "office", "image", "text"}, MaxSelect: 4})
		return app.Save(settings)
	}, func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		settings.Fields.RemoveByName("imap_skip_types")
		return app.Save(settings)
	})
}
