package appapi

import (
	"github.com/pocketbase/pocketbase/core"
)

const pairedAdminField = "is_app_admin"

// IsAppAdmin is true for superuser auth or a users session with is_app_admin.
// Exported because this rule decides who may mint a recovery code for the
// encrypted archive, and a second copy elsewhere could drift from it silently.
func IsAppAdmin(e *core.RequestEvent) bool {
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
