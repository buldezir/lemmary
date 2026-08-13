package appapi

import (
	"encoding/json"

	"github.com/pocketbase/pocketbase/core"
)

func writeJSON(e *core.RequestEvent, status int, data any) error {
	e.Response.Header().Set("Content-Type", "application/json")
	e.Response.WriteHeader(status)
	return json.NewEncoder(e.Response).Encode(data)
}

func writeError(e *core.RequestEvent, status int, detail string) error {
	return writeJSON(e, status, map[string]string{"detail": detail})
}
