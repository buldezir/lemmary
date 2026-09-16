package appapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

const (
	defaultAppName = "Lemmary"
	// Same value as DEFAULT_ACCENT in the SPA: one built-in accent,
	// whichever side has to fall back to it.
	defaultAccent = "#6e2620"

	// What PocketBase seeds Meta with on a fresh install. Both are treated as
	// "not set yet": the name is baked into passkeys, emails, backup names and
	// the admin UI, and the accent is PocketBase's own blue.
	pocketBaseDefaultAppName = "Acme"
	pocketBaseDefaultAccent  = "#1055c9"
)

// brandedDefaults leaves a name or accent someone chose alone.
func brandedDefaults(s *core.Settings) bool {
	if s == nil {
		return false
	}
	changed := false
	if name := strings.TrimSpace(s.Meta.AppName); name == "" || name == pocketBaseDefaultAppName {
		s.Meta.AppName = defaultAppName
		changed = true
	}
	if accent := strings.TrimSpace(s.Meta.AccentColor); accent == "" || accent == pocketBaseDefaultAccent {
		s.Meta.AccentColor = defaultAccent
		changed = true
	}
	return changed
}

// RegisterAppName runs before e.Next so a first-install ReloadSettings persists
// Lemmary's branding, and after it so an existing install that still has a
// placeholder is rewritten on the next boot.
func RegisterAppName(app core.App) {
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		brandedDefaults(e.App.Settings())
		if err := e.Next(); err != nil {
			return err
		}
		if !brandedDefaults(e.App.Settings()) {
			return nil
		}
		if err := e.App.Save(e.App.Settings()); err != nil {
			e.App.Logger().Warn("persist default branding failed; continuing", "error", err)
		}
		return nil
	})
}

func resolvedAppName(app core.App) string {
	if app == nil || app.Settings() == nil {
		return defaultAppName
	}
	name := strings.TrimSpace(app.Settings().Meta.AppName)
	if name == "" || name == pocketBaseDefaultAppName {
		return defaultAppName
	}
	return name
}

func resolvedAccent(app core.App) string {
	if app == nil || app.Settings() == nil {
		return defaultAccent
	}
	if accent := strings.TrimSpace(app.Settings().Meta.AccentColor); accent != "" {
		return accent
	}
	return defaultAccent
}

func handleGetMeta(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		return writeJSON(e, http.StatusOK, map[string]any{
			"app_name": resolvedAppName(app),
			"accent":   resolvedAccent(app),
			// Public: the SPA needs both before anyone has signed in.
			"passkeys":   passkeyLoginAvailable(app, e),
			"ai_managed": rt.Managed(),
			// Public for the same reason ai_managed is: the SPA reads meta
			// before it can know whether the session is an admin's.
			"chatgpt_login": rt.ChatGPTLogin(),
			// Public because it shapes what a regular user sees, while only an
			// admin can change it.
			"always_require_review": rt.AlwaysRequireReview(),
			// Whether a chat may offer the web toggle at all. Having the option
			// is not having the answer: the tools still run only when a user
			// asks for them on the turn.
			"web_search": rt.WebSearchAvailable(),
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
