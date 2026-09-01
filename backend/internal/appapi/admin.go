package appapi

import (
	"github.com/pocketbase/pocketbase/core"
)

const pairedAdminField = "is_app_admin"

// IsAppAdmin is true for PocketBase superuser auth, or a users-collection
// session marked as the paired admin identity (is_app_admin).
//
// Exported because this rule decides who may mint a recovery code for the
// encrypted archive, and a second copy of it in another package is a rule that
// can drift apart from this one without anything failing.
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
