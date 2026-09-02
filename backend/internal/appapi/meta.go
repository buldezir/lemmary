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

	// pocketBaseDefaultAppName is what PocketBase seeds Meta.AppName with on a
	// fresh install (core.newDefaultSettings). Treated as "not set yet": the
	// string is baked into passkeys, emails, backup names and the admin UI, so
	// leaving it would brand a Lemmary instance as Acme until someone renamed it.
	pocketBaseDefaultAppName = "Acme"
)

// RegisterAppName replaces PocketBase's "Acme" placeholder with Lemmary.
//
// Before e.Next so a first-install ReloadSettings persists Lemmary instead of
// Acme. After e.Next so an existing install that still has the placeholder is
// rewritten on the next boot. A custom name is left alone.
func RegisterAppName(app core.App) {
	app.OnBootstrap().BindFunc(func(e *core.BootstrapEvent) error {
		if s := e.App.Settings(); s != nil {
			if name := strings.TrimSpace(s.Meta.AppName); name == "" || name == pocketBaseDefaultAppName {
				s.Meta.AppName = defaultAppName
			}
		}
		if err := e.Next(); err != nil {
			return err
		}
		s := e.App.Settings()
		if s == nil {
			return nil
		}
		if name := strings.TrimSpace(s.Meta.AppName); name != "" && name != pocketBaseDefaultAppName {
			return nil
		}
		s.Meta.AppName = defaultAppName
		if err := e.App.Save(s); err != nil {
			e.App.Logger().Warn("persist default app name failed; continuing", "error", err)
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

func handleGetMeta(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		accent := strings.TrimSpace(app.Settings().Meta.AccentColor)
		if accent == "" {
			accent = defaultAccent
		}
		return writeJSON(e, http.StatusOK, map[string]any{
			"app_name": resolvedAppName(app),
			"accent":   accent,
			// Public: the SPA needs both before anyone has signed in.
			"passkeys":   passkeyLoginAvailable(app, e),
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
