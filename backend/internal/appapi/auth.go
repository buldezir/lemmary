package appapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

func bindAuth(handler func(*core.RequestEvent) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth == nil {
			return writeError(e, http.StatusUnauthorized, "Authentication required.")
		}
		return handler(e)
	}
}

func bindSuperuser(handler func(*core.RequestEvent) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth == nil {
			return writeError(e, http.StatusUnauthorized, "Authentication required.")
		}
		if !e.HasSuperuserAuth() {
			return writeError(e, http.StatusForbidden, "Superuser access required.")
		}
		return handler(e)
	}
}

// bindAdmin allows PocketBase superuser auth or a paired users session
// (users record with is_app_admin).
func bindAdmin(handler func(*core.RequestEvent) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth == nil {
			return writeError(e, http.StatusUnauthorized, "Authentication required.")
		}
		if !isAppAdmin(e) {
			return writeError(e, http.StatusForbidden, "Admin access required.")
		}
		return handler(e)
	}
}
