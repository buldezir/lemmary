package ngxapi

import (
	"errors"
	"net/http/httptest"
	"strings"
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

// This flow is hand-rolled because the Paperless-compatible API answers in its
// own shape, which means every policy PocketBase enforces on its own auth routes
// has to be enforced again here or it is not enforced at all.
//
// MFA is the one that fails in the dangerous direction. With it on, PocketBase
// answers a correct password with an mfaId and demands a second factor; without
// this check /api/token and Basic auth would go on minting full auth tokens for
// the password alone, so enabling MFA would secure the web UI and leave every
// API client an unguarded way in. Under encryption at rest a password accepted
// here is also, through enrollment, a key that unwraps the archive.
func TestCollectionAuthPolicyRefusesPasswordOnlyWhenMFAIsOn(t *testing.T) {
	c := core.NewAuthCollection("users")
	c.PasswordAuth.Enabled = true
	c.MFA.Enabled = true

	err := checkCollectionAuthPolicy(c)
	if err == nil {
		t.Fatal("a password-only sign-in was accepted for a collection requiring MFA")
	}
	if !strings.Contains(err.Error(), "multi-factor") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCollectionAuthPolicyRefusesDisabledPasswordAuth(t *testing.T) {
	c := core.NewAuthCollection("users")
	c.PasswordAuth.Enabled = false

	if err := checkCollectionAuthPolicy(c); err == nil {
		t.Fatal("password auth was accepted while disabled on the collection")
	}
}

// The ordinary configuration must keep working, or every Paperless client breaks.
func TestCollectionAuthPolicyAllowsPlainPasswordAuth(t *testing.T) {
	c := core.NewAuthCollection("users")
	c.PasswordAuth.Enabled = true

	if err := checkCollectionAuthPolicy(c); err != nil {
		t.Fatalf("a plain password collection was refused: %v", err)
	}
}
