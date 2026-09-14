package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Every relation to users rendered as "N/A" in the PocketBase admin UI. The rows
// were correct: a relation cell is drawn from the related collection's
// presentable fields, and users had none, so there was nothing to print. It is a
// display default that reads exactly like data with no owner.
//
// Email rather than name: name is optional here and empty on accounts created
// through the setup wizard or OAuth2, which would put the N/A straight back.
// Presentable is an admin-UI hint only; emailVisibility still governs whether an
// address is exposed.
func init() {
	m.Register(func(app core.App) error {
		return setUsersEmailPresentable(app, true)
	}, func(app core.App) error {
		return setUsersEmailPresentable(app, false)
	})
}

func setUsersEmailPresentable(app core.App, presentable bool) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	field, ok := users.Fields.GetByName("email").(*core.EmailField)
	if !ok {
		// Not the shape this expects; leaving the collection alone beats
		// rewriting a field this migration does not understand.
		return nil
	}
	field.Presentable = presentable
	return app.Save(users)
}
