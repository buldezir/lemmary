package ngxapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

func handleUiSettings(e *core.RequestEvent) error {
	if e.Request.Method == http.MethodHead {
		setNgxHeaders(e)
		if err := checkAPIVersion(e); err != nil {
			return err
		}
		e.Response.WriteHeader(http.StatusOK)
		return nil
	}
	return writeJSON(e, http.StatusOK, mapUiSettings(e.App, e.Auth))
}

func mapUiSettings(app core.App, user *core.Record) map[string]any {
	username := user.GetString("email")
	if username == "" {
		username = user.GetString("name")
	}

	userObj := map[string]any{
		"id":           toNgxID(user.Id),
		"username":     username,
		"is_staff":     false,
		"is_superuser": false,
		"groups":       []int{},
	}
	if name := user.GetString("name"); name != "" {
		parts := splitName(name)
		if parts[0] != "" {
			userObj["first_name"] = parts[0]
		}
		if parts[1] != "" {
			userObj["last_name"] = parts[1]
		}
	}

	return map[string]any{
		"user":        userObj,
		"settings":    defaultUiSettings(app),
		"permissions": defaultPermissions(),
	}
}

func defaultUiSettings(app core.App) map[string]any {
	// PocketBase seeds Meta.AppName as "Acme"; treat it as unset, same as
	// appapi.resolvedAppName. A custom name still wins.
	appTitle := "Lemmary"
	if name := strings.TrimSpace(app.Settings().Meta.AppName); name != "" && name != "Acme" {
		appTitle = name
	}
	return map[string]any{
		"version":          ngxAppVersion,
		"app_title":        appTitle,
		"app_logo":         nil,
		"trash_delay":      30,
		"email_enabled":    false,
		"auditlog_enabled": false,
		"update_checking": map[string]any{
			"backend_setting": "default",
		},
	}
}

func defaultPermissions() []string {
	return []string{
		"view_document", "add_document", "change_document", "delete_document",
		"view_tag", "add_tag", "change_tag", "delete_tag",
		"view_correspondent", "add_correspondent", "change_correspondent", "delete_correspondent",
		"view_documenttype", "add_documenttype", "change_documenttype", "delete_documenttype",
		"view_uisettings", "change_uisettings",
		"view_paperlesstask", "change_paperlesstask",
	}
}
