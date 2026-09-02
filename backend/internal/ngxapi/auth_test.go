package ngxapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
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

// Paperless-ngx tokens do not expire. Clients store POST /api/token/ and never
// refresh. NewAuthToken() follows users.AuthToken.Duration (five days) and is
// why swift-paperless used to die until the server was re-added.
func TestPaperlessAPITokenLastsYearsNotDays(t *testing.T) {
	t.Parallel()

	users := core.NewAuthCollection("users")
	users.AuthToken.Duration = 432000 // five-day PocketBase session default
	record := core.NewRecord(users)
	record.Id = "userpaperless01"
	record.SetTokenKey("test-token-key-for-paperless")

	token, err := mintPaperlessAPIToken(record)
	if err != nil {
		t.Fatalf("mintPaperlessAPIToken() error: %v", err)
	}

	claims, err := security.ParseUnverifiedJWT(token)
	if err != nil {
		t.Fatalf("ParseUnverifiedJWT() error: %v", err)
	}

	if refreshable, _ := claims[core.TokenClaimRefreshable].(bool); refreshable {
		t.Fatal("paperless API tokens must not be refreshable session JWTs")
	}

	exp := jwtExpUnix(claims["exp"])
	if exp == 0 {
		t.Fatalf("missing exp claim: %#v", claims["exp"])
	}

	now := time.Now().Unix()
	nineYears := now + int64(9*365*24*time.Hour/time.Second)
	elevenYears := now + int64(11*365*24*time.Hour/time.Second)
	if exp < nineYears || exp > elevenYears {
		t.Fatalf("exp %d is not ~10 years from now (%d); five-day session tokens are the old bug", exp, now)
	}
}

func TestRequireAuthMissingHeader(t *testing.T) {
	t.Parallel()

	e := newAuthRequestEvent(t, "")
	if err := requireAuth(e); err != nil {
		t.Fatalf("requireAuth() error: %v", err)
	}
	assertUnauthorizedDetail(t, e, "Authentication credentials were not provided.")
}

func TestRequireAuthInvalidToken(t *testing.T) {
	e := newAuthRequestEvent(t, "Token not-a-jwt")
	e.App = bootTestApp(t)

	if err := requireAuth(e); err != nil {
		t.Fatalf("requireAuth() error: %v", err)
	}
	assertUnauthorizedDetail(t, e, "Invalid token.")
}

func newAuthRequestEvent(t *testing.T, authHeader string) *core.RequestEvent {
	t.Helper()
	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest(http.MethodGet, "/api/documents/", nil)
	e.Request.Header.Set("Accept", "application/json; version=9")
	if authHeader != "" {
		e.Request.Header.Set("Authorization", authHeader)
	}
	e.Response = httptest.NewRecorder()
	return e
}

func assertUnauthorizedDetail(t *testing.T, e *core.RequestEvent, want string) {
	t.Helper()
	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rec.Body.String())
	}
	if body.Detail != want {
		t.Fatalf("detail = %q, want %q", body.Detail, want)
	}
}

func bootTestApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	return app
}

func jwtExpUnix(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case json.Number:
		i, _ := n.Int64()
		return i
	default:
		return 0
	}
}
