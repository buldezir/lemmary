package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Drop env_applied, whose whole subject has been retired.
//
// Migration 1730000013 added it to answer "has this environment variable
// changed since the last boot that acted on it", so that one code path could
// serve both an operator who expresses a plan change by recreating a container
// and an admin who expects a Settings edit to survive a restart.
//
// Those are now two named modes rather than one inferred rule. Self-hosted
// seeds on the first boot and never again; managed (AI_MANAGED=1) re-applies
// the operator-owned settings on every boot and hides them from the Settings
// page. Neither asks whether anything changed, so there is nothing left to
// compare against and the column only stores digests nobody reads.
//
// The down direction adds the field back empty rather than restoring its
// contents, which is all a rollback needs: an empty digest map reads as "no
// variable has been applied yet", and the code that used it treated that as a
// reason to apply the environment once. Dropping to a state that re-applies is
// the safe direction to roll back into.
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
