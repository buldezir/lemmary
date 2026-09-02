package appwire

import (
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

// coopHeader relaxes the Cross-Origin-Opener-Policy PocketBase sets by default.
//
// PocketBase 0.40 added "Cross-Origin-Opener-Policy: same-origin" to the
// security headers it puts on every route, the SPA document included. Under
// that value the browser drops the opener relationship as soon as a popup
// navigates cross-origin, which is precisely what OAuth2 sign-in does: the
// window we opened goes off to Google, the handle in loginWithOAuth2 is
// severed, and popup.closed starts reporting true. watchOAuthPopup reads that
// as the user giving up and fails the login with "Sign-in was cancelled."
// roughly two seconds into a consent screen that is still on the way.
//
// "same-origin-allow-popups" keeps the half of the protection that matters
// here -- a cross-origin document that opens us still loses its handle on us --
// while letting us keep handles on the popups we open ourselves. It is the
// value the popup-based OAuth2 flow needs, and what other apps in this
// position use.
//
// The default priority puts this inside PocketBase's own security-headers
// middleware (its priority is negative), so this Set lands after the one it is
// overriding but still before any handler writes a body.
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
