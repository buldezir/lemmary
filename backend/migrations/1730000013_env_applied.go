package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// env_applied records, per environment variable, a digest of the value this
// install last acted on.
//
// Without it the environment could only ever seed: app_settings was written on
// first boot and the database was authoritative afterwards, so changing
// OPENAI_MODEL on an existing install did nothing. Making the environment win
// on every boot instead would have been the other extreme — it would silently
// revert a change made in the Settings page on the next restart, for every
// install that has the variable set, which .env.example ships.
//
// Storing what was last applied gives the rule that actually matches how these
// installs are operated: an environment variable that *changed* is applied, and
// one that did not is left alone. An orchestrator expresses a plan change by
// recreating a container with different environment, and that lands; a person
// editing Settings keeps their edit until somebody changes the environment
// again.
//
// Digests rather than values: several of the tracked variables are API keys, and
// a copy of a secret in a second column is a second place it can leak from. A
// digest answers "did this change?", which is the only question asked of it.
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
