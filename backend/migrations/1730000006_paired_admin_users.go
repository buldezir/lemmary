package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

const pairedAdminField = "is_app_admin"

func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}

		// This app has no self-signup; only superusers / server-side code create users.
		users.CreateRule = nil

		if users.Fields.GetByName(pairedAdminField) == nil {
			users.Fields.Add(&core.BoolField{
				Name:   pairedAdminField,
				Hidden: true,
			})
		}
		if err := app.Save(users); err != nil {
			return err
		}

		return backfillPairedAdmins(app)
	}, func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return nil
		}
		// Restore PocketBase default (empty rule = anyone can create).
		users.CreateRule = types.Pointer("")
		if f := users.Fields.GetByName(pairedAdminField); f != nil {
			users.Fields.RemoveById(f.GetId())
		}
		return app.Save(users)
	})
}

// backfillPairedAdmins marks existing users records whose email matches a
// real _superusers account (legacy email-pairing installs).
func backfillPairedAdmins(app core.App) error {
	supers, err := app.FindAllRecords(core.CollectionNameSuperusers)
	if err != nil {
		return err
	}
	for _, super := range supers {
		email := super.Email()
		if email == "" || email == core.DefaultInstallerEmail {
			continue
		}
		user, err := app.FindAuthRecordByEmail("users", email)
		if err != nil {
			continue
		}
		if user.GetBool(pairedAdminField) {
			continue
		}
		user.Set(pairedAdminField, true)
		if err := app.Save(user); err != nil {
			return err
		}
	}
	return nil
}
