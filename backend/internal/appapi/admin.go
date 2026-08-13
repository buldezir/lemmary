package appapi

import (
	"github.com/pocketbase/pocketbase/core"
)

const pairedAdminField = "is_app_admin"

// isAppAdmin is true for PocketBase superuser auth, or a users-collection
// session marked as the paired admin identity (is_app_admin).
func isAppAdmin(e *core.RequestEvent) bool {
	if e.Auth == nil {
		return false
	}
	if e.HasSuperuserAuth() {
		return true
	}
	if e.Auth.Collection().Name != "users" {
		return false
	}
	return e.Auth.GetBool(pairedAdminField)
}
