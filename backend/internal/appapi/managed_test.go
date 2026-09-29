package appapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

// Not parallel: NewRuntime writes the process-global managed flag.
func TestUnlessManagedRefusesOnlyAManagedInstance(t *testing.T) {
	prev := aiprovider.Managed()
	t.Cleanup(func() { aiprovider.SetManaged(prev) })

	for _, tc := range []struct {
		name    string
		managed bool
		want    int
	}{
		{"managed", true, http.StatusForbidden},
		{"self-hosted", false, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := config.NewRuntime(config.AIEnv{Managed: tc.managed})
			rec := httptest.NewRecorder()
			e := &core.RequestEvent{}
			e.Response = rec
			e.Request = httptest.NewRequest(http.MethodPost, "/api/app/ocr/test", nil)

			if err := unlessManaged(rt)(e); err != nil {
				t.Fatalf("middleware: %v", err)
			}
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}
