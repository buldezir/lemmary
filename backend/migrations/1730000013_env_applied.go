package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// env_applied records, per environment variable, a digest of the value this
// install last acted on, so a variable that changed is applied and one that did
// not is left alone: an orchestrator expresses a plan change by recreating a
// container, and a person editing Settings keeps their edit until the
// environment changes again.
//
// Digests rather than values: several of the tracked variables are API keys, and
// a copy of a secret in a second column is a second place it can leak from.
func init() {
	m.Register(func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		settings.Fields.Add(&core.JSONField{Name: "env_applied", MaxSize: 20000})
		return app.Save(settings)
	}, func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		settings.Fields.RemoveByName("env_applied")
		return app.Save(settings)
	})
}
