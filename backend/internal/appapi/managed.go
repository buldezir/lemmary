package appapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

// Shown when a write hits a setting the operator owns.
const managedMessage = "AI configuration is managed by your hosting provider and cannot be changed here."

// Shown when a feature a managed instance does not offer is called anyway.
const unavailableWhenManagedMessage = "Not available on an instance run by a hosting provider."

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
