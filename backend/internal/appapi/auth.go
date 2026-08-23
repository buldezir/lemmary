package appapi

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

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

// ownerClientError is a resolution failure the caller can act on, so it maps to
// 4xx rather than 500.
type ownerClientError struct {
	msg string
}

func (e *ownerClientError) Error() string {
	return e.msg
}

func writeOwnerError(e *core.RequestEvent, err error) error {
	var clientErr *ownerClientError
	if errors.As(err, &clientErr) {
		return writeError(e, http.StatusBadRequest, clientErr.Error())
	}
	return writeError(e, http.StatusInternalServerError, "Failed to resolve document owner.")
}

// resolveOwnerUserID returns the users-collection record id that owns the
// documents this request acts on. A superuser session has no users record of its
// own, so it is mapped to the paired account by email.
func resolveOwnerUserID(app core.App, e *core.RequestEvent) (string, error) {
	if e.Auth == nil {
		return "", &ownerClientError{msg: "Authentication required."}
	}
	if e.Auth.Collection().Name == "users" {
		return e.Auth.Id, nil
	}
	email := strings.TrimSpace(e.Auth.Email())
	if email == "" {
		return "", &ownerClientError{msg: "Admin account has no email; cannot resolve document owner."}
	}
	user, err := app.FindAuthRecordByEmail("users", email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", &ownerClientError{msg: "No paired users account for this admin; sign in with the admin user account."}
		}
		return "", err
	}
	return user.Id, nil
}
