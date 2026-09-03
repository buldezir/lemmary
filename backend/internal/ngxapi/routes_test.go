package ngxapi

import (
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// TestAcknowledgeRoutesAreMounted is the half a handler test cannot cover: the
// bug reported was POST /api/acknowledge_tasks/ answering 404, which is routing
// rather than logic. Both paths are here because paperless-ngx moved the
// endpoint in 2.14 and a client picks one from the version the server
// advertises, never trying the other.
func TestAcknowledgeRoutesAreMounted(t *testing.T) {
	app := bootSchemaTestApp(t)
	Register(app, nil)

	e := &core.ServeEvent{App: app, Router: router.NewRouter[*core.RequestEvent](nil)}
	if err := app.OnServe().Trigger(e); err != nil {
		t.Fatalf("serve hooks: %v", err)
	}

	for _, path := range []string{
		"/api/acknowledge_tasks/", "/api/acknowledge_tasks",
		"/api/tasks/acknowledge/", "/api/tasks/acknowledge",
	} {
		if !e.Router.HasRoute(http.MethodPost, path) {
			t.Fatalf("POST %s is not mounted", path)
		}
	}
}
