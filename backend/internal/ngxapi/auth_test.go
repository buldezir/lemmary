package ngxapi

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

func TestTokenGETReturns405ForSupportedAPIVersion(t *testing.T) {
	t.Parallel()

	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest("GET", "/api/token/", nil)
	e.Request.Header.Set("Accept", "application/json; version=9")
	e.Response = httptest.NewRecorder()

	if err := handleTokenMethodNotAllowed(e); err != nil {
		t.Fatalf("handleTokenMethodNotAllowed() error: %v", err)
	}

	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != 405 {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("X-Api-Version"); got != "9" {
		t.Fatalf("X-Api-Version = %q, want 9", got)
	}
	if got := rec.Header().Get("X-Version"); got != "0.1.0" {
		t.Fatalf("X-Version = %q, want 0.1.0", got)
	}
}

func TestTokenGETReturns406ForUnsupportedAPIVersion(t *testing.T) {
	t.Parallel()

	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest("GET", "/api/token/", nil)
	e.Request.Header.Set("Accept", "application/json; version=3")
	e.Response = httptest.NewRecorder()

	// The sentinel error is what stops handlers from running on after a 406;
	// returning nil here used to let state-changing endpoints execute anyway.
	if err := handleTokenMethodNotAllowed(e); !errors.Is(err, errUnsupportedAPIVersion) {
		t.Fatalf("handleTokenMethodNotAllowed() error = %v, want errUnsupportedAPIVersion", err)
	}

	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != 406 {
		t.Fatalf("status = %d, want 406", rec.Code)
	}
}
