package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// An account's own preferences as one object, so a new one needs no migration.
// On users so they follow the account to every device; the account writes them
// through the users UpdateRule, which already lets it edit its own record. The
// keys are the frontend's (lib/userSettings.ts), which reads every one
// defensively: the database checks only the size.
func init() {
	m.Register(addUserPreferencesField, func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return nil
		}
		if f := users.Fields.GetByName("preferences"); f != nil {
			users.Fields.RemoveById(f.GetId())
		}
		return app.Save(users)
	})
}

func addUserPreferencesField(app core.App) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	if users.Fields.GetByName("preferences") != nil {
		return nil
	}
	users.Fields.Add(&core.JSONField{Name: "preferences", MaxSize: 4096})
	return app.Save(users)
}
