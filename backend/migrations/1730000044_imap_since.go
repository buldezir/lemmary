package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds imap_since: when the configured mailbox last changed. The scan ignores
// mail received before it, so pointing Lemmary at a full mailbox imports only
// what arrives from then on; older mail is a Management backfill.
func init() {
	m.Register(func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		if settings.Fields.GetByName("imap_since") != nil {
			return nil
		}
		settings.Fields.Add(&core.DateField{Name: "imap_since"})
		return app.Save(settings)
	}, func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		settings.Fields.RemoveByName("imap_since")
		return app.Save(settings)
	})
}
