package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Every relation to users rendered as "N/A" in the PocketBase admin UI --
// documents, chat_sessions, jobs, all of them.
//
// The rows were correct; nothing was ever unowned. A relation cell is drawn
// from the related collection's *presentable* fields, and users had none, so
// there was nothing for the UI to print. It is a display default rather than a
// missing value, but it makes the admin UI useless for the one question it is
// usually opened to answer -- whose record is this -- and reads exactly like
// data with no owner, which is a bad thing to have to guess about.
//
// Email rather than name: name is optional here and empty on accounts created
// through the setup wizard or OAuth2, which would put the N/A straight back.
// Presentable is an admin-UI hint only; it does not widen what the API returns,
// and emailVisibility still governs whether an address is exposed to anyone.
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
