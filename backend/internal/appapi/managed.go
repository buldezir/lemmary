package appapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

// Shown when a write hits a setting the operator owns.
const managedMessage = "AI configuration is managed by your hosting provider and cannot be changed here."

// refuseWhenManaged is the real guard: hiding those Settings sections is a
// courtesy, and the endpoints remain reachable with any admin session.
func refuseWhenManaged(e *core.RequestEvent, rt *config.Runtime) (bool, error) {
	if !rt.Managed() {
		return false, nil
	}
	return true, writeError(e, http.StatusForbidden, managedMessage)
}
