package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Lets a users file token live 15 minutes instead of PocketBase's 3. MCP's
// get_document hands out file links that an agent passes on to a person, who
// rarely clicks within 3 minutes. The browser app mints a token per file view,
// so for it the change only lengthens how long a copied link keeps working.
func init() {
	m.Register(func(app core.App) error {
		return setUsersFileTokenDuration(app, 15*60)
	}, func(app core.App) error {
		return setUsersFileTokenDuration(app, 3*60)
	})
}

func setUsersFileTokenDuration(app core.App, seconds int64) error {
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	users.FileToken.Duration = seconds
	return app.Save(users)
}
