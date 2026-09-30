package appapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/i18n"
)

// Shown when a write hits a setting the operator owns.
const managedMessage = "AI configuration is managed by your hosting provider and cannot be changed here."

// Shown when a feature a managed instance does not offer is called anyway.
const unavailableWhenManagedMessage = "Not available on an instance run by a hosting provider."

const readOnlyMessage = "This workspace is read-only now."

// refuseWhenManaged is the real guard: hiding those Settings sections is a
// courtesy, and the endpoints remain reachable with any admin session.
func refuseWhenManaged(e *core.RequestEvent, rt *config.Runtime) (bool, error) {
	if !rt.Managed() {
		return false, nil
	}
	return true, writeError(e, http.StatusForbidden, managedMessage)
}

// unlessManaged rather than skipping registration: an unregistered GET falls
// through to the SPA and answers 200 with index.html.
func unlessManaged(rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if rt.Managed() {
			return writeError(e, http.StatusForbidden, unavailableWhenManagedMessage)
		}
		return e.Next()
	}
}

// lockSuperusersWhenManaged shuts PocketBase's own admin surface: the
// dashboard, every _superusers route, and any request a superuser token
// carries. The managed guards sit on this package's routes, so a superuser
// writing settings or ai_providers through the collection API would go round
// them, live until the next boot. The superuser CLI never touches HTTP and is
// how the host creates and resets the account.
func lockSuperusersWhenManaged(rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if !rt.Managed() {
			return e.Next()
		}
		if path := e.Request.URL.Path; path == "/_" || strings.HasPrefix(path, "/_/") {
			return writeError(e, http.StatusNotFound, "Not found.")
		}
		if e.HasSuperuserAuth() || routesToSuperusers(e) {
			return writeError(e, http.StatusForbidden, unavailableWhenManagedMessage)
		}
		return e.Next()
	}
}

// By name or id, since PocketBase routes accept either.
func routesToSuperusers(e *core.RequestEvent) bool {
	name := e.Request.PathValue("collection")
	if name == "" {
		return false
	}
	collection, err := e.App.FindCachedCollectionByNameOrId(name)
	return err == nil && collection.Name == core.CollectionNameSuperusers
}

// refuseWritesWhenReadOnly gates by request rather than by record hook: some
// writes are raw SQL, and a hook would also refuse the sign-in counter a
// passkey login saves. Work queued before the deadline still runs.
func refuseWritesWhenReadOnly(rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if !rt.ReadOnly() || readsOnly(e.Request) {
			return e.Next()
		}
		// Both fields: the PocketBase SDK shows message, apiClient shows detail.
		msg := i18n.T(i18n.FromRequest(e.Request), readOnlyMessage)
		return writeJSON(e, http.StatusForbidden, map[string]any{
			"status": http.StatusForbidden, "message": msg, "detail": msg, "data": map[string]any{},
		})
	}
}

// The POSTs here sign in or subscribe; none of them changes a document. By
// exact path: OAuth2 and OTP can create a user, so they stay refused.
func readsOnly(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	case http.MethodPost:
		switch r.URL.Path {
		case "/api/collections/users/auth-with-password", "/api/collections/users/auth-refresh",
			"/api/app/passkeys/login/begin", "/api/app/passkeys/login/finish",
			"/api/realtime", "/api/files/token":
			return true
		}
	}
	return false
}
