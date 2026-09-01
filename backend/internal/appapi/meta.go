package appapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

const (
	defaultAppName = "Lemmary"
	defaultAccent  = "#111827" // gray-900, matches previous logo background
)

func handleGetMeta(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		name := strings.TrimSpace(app.Settings().Meta.AppName)
		if name == "" {
			name = defaultAppName
		}
		accent := strings.TrimSpace(app.Settings().Meta.AccentColor)
		if accent == "" {
			accent = defaultAccent
		}
		return writeJSON(e, http.StatusOK, map[string]any{
			"app_name": name,
			"accent":   accent,
			// Whether the login screen should offer the passkey button. This is
			// the only public, pre-session endpoint, which is why the flag lives
			// here rather than behind auth.
			//
			// Two conditions, both instance-wide. The address has to be one a
			// credential can be bound to at all — see internal/passkey/rp.go. And
			// somebody has to have enrolled: a button that opens an empty
			// authenticator dialog and ends in NotAllowedError is a worse first
			// impression than no button. Instance-wide rather than per-account is
			// deliberate — it answers "does this install use passkeys", never
			// "does this person have one", so it is not an enumeration signal.
			"passkeys": passkeyLoginAvailable(app, e),
			// Whether the operator owns AI configuration. Public for the same
			// reason the passkey flag is: the SPA needs it before anyone has
			// signed in, to know which Settings sections exist at all. It
			// leaks nothing — it says who configures this install, never how.
			"ai_managed": rt.Managed(),
		})
	}
}

func handleGetMe(_ core.App) func(*core.RequestEvent) error {
	return bindAuth(func(e *core.RequestEvent) error {
		email := ""
		if e.Auth != nil {
			email = e.Auth.Email()
		}
		return writeJSON(e, http.StatusOK, map[string]any{
			"email":    email,
			"is_admin": IsAppAdmin(e),
		})
	})
}
