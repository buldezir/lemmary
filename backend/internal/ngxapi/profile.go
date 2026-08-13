package ngxapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

func handleProfile(e *core.RequestEvent) error {
	if e.Request.Method == http.MethodHead {
		setNgxHeaders(e)
		if err := checkAPIVersion(e); err != nil {
			return err
		}
		e.Response.WriteHeader(http.StatusOK)
		return nil
	}
	return writeJSON(e, http.StatusOK, mapProfile(e.Auth))
}

func mapProfile(user *core.Record) map[string]any {
	profile := map[string]any{
		"email":               user.GetString("email"),
		"has_usable_password": true,
		"is_mfa_enabled":      false,
		"social_accounts":     []any{},
	}
	if name := user.GetString("name"); name != "" {
		parts := splitName(name)
		if parts[0] != "" {
			profile["first_name"] = parts[0]
		}
		if parts[1] != "" {
			profile["last_name"] = parts[1]
		}
	}
	return profile
}

func splitName(name string) [2]string {
	parts := [2]string{"", ""}
	fields := strings.Fields(name)
	if len(fields) > 0 {
		parts[0] = fields[0]
	}
	if len(fields) > 1 {
		parts[1] = fields[len(fields)-1]
	}
	return parts
}
