package appapi

import (
	"encoding/json"
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
// (email also present in _superusers).
func bindAdmin(app core.App, handler func(*core.RequestEvent) error) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if e.Auth == nil {
			return writeError(e, http.StatusUnauthorized, "Authentication required.")
		}
		if !isAppAdmin(app, e) {
			return writeError(e, http.StatusForbidden, "Admin access required.")
		}
		return handler(e)
	}
}

func writeJSON(e *core.RequestEvent, status int, data any) error {
	e.Response.Header().Set("Content-Type", "application/json")
	e.Response.WriteHeader(status)
	return json.NewEncoder(e.Response).Encode(data)
}

func writeError(e *core.RequestEvent, status int, detail string) error {
	return writeJSON(e, status, map[string]string{"detail": detail})
}
