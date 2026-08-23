package authguard

import (
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/hook"
)

const collectionRecordsAuthGuardId = "lemmaryCollectionRecordsAuthGuard"

// Register forces PocketBase collection record API requests to have valid auth.
//
// PocketBase intentionally treats missing or invalid Authorization headers as
// anonymous access, which can return 200 with an empty list when rules filter
// everything out. For this app, collection record APIs are always private.
func Register(app core.App) {
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			e.Router.Bind(&hook.Handler[*core.RequestEvent]{
				Id:       collectionRecordsAuthGuardId,
				Priority: apis.DefaultLoadAuthTokenMiddlewarePriority + 1,
				Func:     requireCollectionRecordsAuth,
			})
			return e.Next()
		},
	})
}

func requireCollectionRecordsAuth(e *core.RequestEvent) error {
	if !isCollectionRecordRequest(e.Request) {
		return e.Next()
	}

	if e.Auth != nil {
		return e.Next()
	}

	token := extractAuthToken(e.Request.Header.Get("Authorization"))
	if token != "" {
		record, err := e.App.FindAuthRecordByToken(token, core.TokenTypeAuth)
		if err == nil && record != nil {
			e.Auth = record
			return e.Next()
		}
	}

	return e.UnauthorizedError("The request requires valid record authorization token.", nil)
}

func isCollectionRecordRequest(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return false
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	return len(parts) >= 4 &&
		parts[0] == "api" &&
		parts[1] == "collections" &&
		parts[3] == "records"
}

func extractAuthToken(header string) string {
	header = strings.TrimSpace(header)
	lower := strings.ToLower(header)

	switch {
	case strings.HasPrefix(lower, "bearer "):
		return strings.TrimSpace(header[7:])
	case strings.HasPrefix(lower, "token "):
		return strings.TrimSpace(header[6:])
	default:
		return header
	}
}
