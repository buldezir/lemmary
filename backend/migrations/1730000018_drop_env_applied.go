package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Drop env_applied. Self-hosted seeds once; managed re-applies on every boot.
// Neither compares against a digest, so the column is unread.
//
// Down adds the field back empty: an empty map reads as "nothing applied yet",
// which the old code treated as a reason to apply once — the safe rollback.
func init() {
	m.Register(func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		settings.Fields.RemoveByName("env_applied")
		return app.Save(settings)
	}, func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		settings.Fields.Add(&core.JSONField{Name: "env_applied", MaxSize: 20000})
		return app.Save(settings)
	})
}
