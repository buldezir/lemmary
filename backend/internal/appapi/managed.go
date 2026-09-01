package appapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

// managedMessage is what an admin sees when they reach a setting the operator
// owns. It names who to ask rather than only saying no, because on a managed
// instance there is nothing the person reading it can do in this UI.
const managedMessage = "AI configuration is managed by your hosting provider and cannot be changed here."

// refuseWhenManaged blocks a write to something AI_MANAGED puts under the
// operator's control, and reports whether it did.
//
// The SPA already hides these sections on a managed instance, and that is not
// the guard — it is a courtesy. This is the guard: the endpoints are reachable
// with any admin session regardless of what the page chose to render, and a
// tenant who swapped in their own API key would move the AI bill onto an
// account the operator is billing for it.
func refuseWhenManaged(e *core.RequestEvent, rt *config.Runtime) (bool, error) {
	if !rt.Managed() {
		return false, nil
	}
	return true, writeError(e, http.StatusForbidden, managedMessage)
}
