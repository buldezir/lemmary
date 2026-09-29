package appapi

import (
	"net/http"

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
