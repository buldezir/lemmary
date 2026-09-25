package ngxapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestAppConfigReturnsArray(t *testing.T) {
	t.Parallel()

	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest(http.MethodGet, "/api/config", nil)
	e.Request.Header.Set("Accept", "application/json; version=9")
	e.Response = httptest.NewRecorder()
	e.Auth = &core.Record{}

	if err := handleAppConfig(e); err != nil {
		t.Fatalf("handleAppConfig() error: %v", err)
	}

	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not a JSON array: %v", err)
	}
	if len(body) != 1 {
		t.Fatalf("len = %d, want 1", len(body))
	}
	if body[0]["id"] != float64(1) {
		t.Fatalf("id = %v, want 1", body[0]["id"])
	}
}
