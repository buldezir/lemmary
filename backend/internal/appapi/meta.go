package appapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

const (
	defaultAppName = "Lemmary"
	defaultAccent  = "#111827" // gray-900, matches previous logo background
)

func handleGetMeta(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		name := strings.TrimSpace(app.Settings().Meta.AppName)
		if name == "" {
			name = defaultAppName
		}
		accent := strings.TrimSpace(app.Settings().Meta.AccentColor)
		if accent == "" {
			accent = defaultAccent
		}
		return writeJSON(e, http.StatusOK, map[string]string{
			"app_name": name,
			"accent":   accent,
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
			"is_admin": isAppAdmin(e),
		})
	})
}
