package appwire

import (
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

// PocketBase sets "Cross-Origin-Opener-Policy: same-origin" on every route,
// which severs the opener handle the moment an OAuth2 popup navigates to the
// provider; watchOAuthPopup then reads popup.closed as the user cancelling.
// "same-origin-allow-popups" still denies a cross-origin document a handle on
// us. The default priority puts this inside PocketBase's own security-headers
// middleware, so the Set lands after the value it overrides.
const (
	coopHeaderName  = "Cross-Origin-Opener-Policy"
	coopHeaderValue = "same-origin-allow-popups"
)

func registerCOOPHeader(app *pocketbase.PocketBase) {
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		e.Router.BindFunc(func(e *core.RequestEvent) error {
			e.Response.Header().Set(coopHeaderName, coopHeaderValue)
			return e.Next()
		})
		return e.Next()
	})
}
