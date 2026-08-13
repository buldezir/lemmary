package ngxapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

func handleAppConfig(e *core.RequestEvent) error {
	// paperless-ngx returns a list; swift-paperless decodes [ServerConfiguration].
	return writeJSON(e, http.StatusOK, []map[string]any{
		{"id": 1},
	})
}
