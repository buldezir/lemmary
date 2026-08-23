package ngxapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

func handleSchema(e *core.RequestEvent) error {
	if err := checkAPIVersion(e); err != nil {
		return err
	}
	if e.Request.Method == http.MethodHead {
		setNgxHeaders(e)
		e.Response.WriteHeader(http.StatusOK)
		return nil
	}
	return writeJSON(e, http.StatusOK, map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "Lemmary API",
			"version": ngxAppVersion,
		},
	})
}
