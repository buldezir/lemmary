package appapi

import (
	"encoding/json"
	"fmt"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/i18n"
)

func writeJSON(e *core.RequestEvent, status int, data any) error {
	e.Response.Header().Set("Content-Type", "application/json")
	e.Response.WriteHeader(status)
	return json.NewEncoder(e.Response).Encode(data)
}

func writeError(e *core.RequestEvent, status int, detail string) error {
	return writeJSON(e, status, map[string]string{"detail": i18n.T(i18n.FromRequest(e.Request), detail)})
}

func writeErrorf(e *core.RequestEvent, status int, format string, args ...any) error {
	detail := fmt.Sprintf(i18n.T(i18n.FromRequest(e.Request), format), args...)
	return writeJSON(e, status, map[string]string{"detail": detail})
}
