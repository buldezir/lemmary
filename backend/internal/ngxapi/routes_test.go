package ngxapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
)

// serveNgx mounts the API on PocketBase's real router, so a pattern that
// clashes with PocketBase's own routes fails here rather than at boot.
func serveNgx(t *testing.T) http.Handler {
	t.Helper()
	app := bootSchemaTestApp(t)
	Register(app, nil)

	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	if err := app.OnServe().Trigger(&core.ServeEvent{App: app, Router: r}); err != nil {
		t.Fatalf("serve hooks: %v", err)
	}
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}
	return mux
}

// Unauthenticated, so a mounted route answers 401 and anything else 404. The
// acknowledge paths are both here because paperless-ngx moved the endpoint in
// 2.14 and a client picks one from the version the server advertises.
func TestRoutesAnswerOnlyTheirOwnPaths(t *testing.T) {
	mux := serveNgx(t)

	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/api/acknowledge_tasks/", http.StatusUnauthorized},
		{http.MethodPost, "/api/acknowledge_tasks", http.StatusUnauthorized},
		{http.MethodPost, "/api/tasks/acknowledge/", http.StatusUnauthorized},
		{http.MethodPost, "/api/tasks/acknowledge", http.StatusUnauthorized},
		{http.MethodGet, "/api/documents/1/suggestions/", http.StatusUnauthorized},
		{http.MethodGet, "/api/documents/1/preview/", http.StatusUnauthorized},
		{http.MethodGet, "/api/remote_version/", http.StatusOK},
		{http.MethodGet, "/api/", http.StatusFound},
		// Each of these was answered by the route one level up, whose
		// trailing slash made it a subtree: the document, the task list.
		{http.MethodGet, "/api/documents/1/metadata/", http.StatusNotFound},
		{http.MethodGet, "/api/tasks/summary/", http.StatusNotFound},
		{http.MethodGet, "/api/auth/headless/app/v1/config", http.StatusNotFound},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d: %s", c.method, c.path, rec.Code, c.want, rec.Body.String())
		}
	}
}
