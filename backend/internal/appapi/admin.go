package appapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// isAppAdmin is true for PocketBase superuser auth, or a users-collection
// session whose email also exists in _superusers (paired admin identity).
func isAppAdmin(app core.App, e *core.RequestEvent) bool {
	if e.Auth == nil {
		return false
	}
	if e.HasSuperuserAuth() {
		return true
	}
	if e.Auth.Collection().Name != "users" {
		return false
	}
	email := strings.TrimSpace(e.Auth.Email())
	if email == "" {
		return false
	}
	ok, err := emailIsSuperuser(app, email)
	return err == nil && ok
}

func emailIsSuperuser(app core.App, email string) (bool, error) {
	email = strings.TrimSpace(email)
	if email == "" || email == core.DefaultInstallerEmail {
		return false, nil
	}
	total, err := app.CountRecords(core.CollectionNameSuperusers, dbx.HashExp{
		"email": email,
	})
	if err != nil {
		return false, err
	}
	return total > 0, nil
}

func handleGetMe(app core.App) func(*core.RequestEvent) error {
	return bindAuth(func(e *core.RequestEvent) error {
		email := ""
		if e.Auth != nil {
			email = e.Auth.Email()
		}
		return writeJSON(e, http.StatusOK, map[string]any{
			"email":    email,
			"is_admin": isAppAdmin(app, e),
		})
	})
}
