package ngxapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

const (
	ngxAPIVersion = 9
	ngxAppVersion = "0.1.0"
)

var supportedAPIVersions = []int{9, 10}

// errUnsupportedAPIVersion means checkAPIVersion already wrote the 406
// response. It must be non-nil so callers actually stop: returning the
// encoder's nil error here used to let the handler keep executing — a DELETE
// would still delete the document after the client saw a 406. PocketBase's
// ErrorHandler sees the committed response and writes nothing more.
var errUnsupportedAPIVersion = errors.New("unsupported paperless-ngx API version")

func checkAPIVersion(e *core.RequestEvent) error {
	version := parseAcceptVersion(e.Request.Header.Get("Accept"))
	if version == 0 {
		return nil
	}
	if !slices.Contains(supportedAPIVersions, version) {
		setNgxHeaders(e)
		e.Response.Header().Set("Content-Type", "application/json")
		e.Response.WriteHeader(http.StatusNotAcceptable)
		_ = json.NewEncoder(e.Response).Encode(map[string]string{
			"detail": `Invalid version in "Accept" header.`,
		})
		return errUnsupportedAPIVersion
	}
	return nil
}

func parseAcceptVersion(accept string) int {
	for _, part := range strings.Split(accept, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "version=") {
			v, _ := strconv.Atoi(strings.TrimPrefix(part, "version="))
			return v
		}
	}
	return 0
}

func setNgxHeaders(e *core.RequestEvent) {
	e.Response.Header().Set("X-Api-Version", strconv.Itoa(ngxAPIVersion))
	e.Response.Header().Set("X-Version", ngxAppVersion)
}

func handleRemoteVersion(e *core.RequestEvent) error {
	return writeJSON(e, http.StatusOK, map[string]any{
		"version":          "v" + ngxAppVersion,
		"update_available": false,
	})
}
