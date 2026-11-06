package ngxapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/apitoken"
)

func TestTokenGETReturns405ForSupportedAPIVersion(t *testing.T) {
	t.Parallel()

	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest(http.MethodGet, "/api/token/", nil)
	e.Request.Header.Set("Accept", "application/json; version=9")
	e.Response = httptest.NewRecorder()

	if err := handleTokenMethodNotAllowed(e); err != nil {
		t.Fatalf("handleTokenMethodNotAllowed() error: %v", err)
	}

	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != 405 {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	if got := rec.Header().Get("X-Api-Version"); got != "10" {
		t.Fatalf("X-Api-Version = %q, want 10", got)
	}
	if got := rec.Header().Get("X-Version"); got != "3.0.0" {
		t.Fatalf("X-Version = %q, want 3.0.0", got)
	}
}

func TestTokenGETReturns406ForUnsupportedAPIVersion(t *testing.T) {
	t.Parallel()

	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest(http.MethodGet, "/api/token/", nil)
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

// The Paperless-compatible API is hand-rolled, so every policy PocketBase
// enforces on its own auth routes has to be enforced again here.
//
// With MFA on, PocketBase answers a correct password with an mfaId and demands a
// second factor; without this check /api/token and Basic auth would go on
// minting full auth tokens for the password alone.
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

// A Paperless app token opens this API and nothing else: it must not be a
// PocketBase session, which every other route would accept with the account's
// full authority.
func TestGenerateAuthTokenMintsAPITokenForCaller(t *testing.T) {
	t.Parallel()

	app := bootSchemaTestApp(t)
	userID := createUser(t, app, "paperless@example.com")
	user, err := app.FindRecordById("users", userID)
	if err != nil {
		t.Fatal(err)
	}
	e := newAuthRequestEvent(t, "")
	e.App = app
	e.Request.Method = http.MethodPost
	e.Auth = user

	if err := handleGenerateAuthToken(e); err != nil {
		t.Fatalf("handleGenerateAuthToken() error: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var token string
	if err := json.Unmarshal(rec.Body.Bytes(), &token); err != nil {
		t.Fatalf("paperless-ngx answers a bare JSON string: %v (%s)", err, rec.Body.String())
	}

	if _, err := app.FindAuthRecordByToken(token, core.TokenTypeAuth); err == nil {
		t.Fatal("the paperless token is a PocketBase session token")
	}

	signedIn := newAuthRequestEvent(t, "Token "+token)
	signedIn.App = app
	if err := requireAuth(signedIn); err != nil {
		t.Fatalf("requireAuth() error = %v", err)
	}
	if signedIn.Auth == nil || signedIn.Auth.Id != userID {
		t.Fatalf("signed in as %v, want %s", signedIn.Auth, userID)
	}
}

func TestGenerateAuthTokenRefusesAPaperlessToken(t *testing.T) {
	t.Parallel()

	app := bootSchemaTestApp(t)
	userID := createUser(t, app, "spare@example.com")
	token, err := apitoken.Mint(app, userID, apitoken.ScopePaperless, "")
	if err != nil {
		t.Fatal(err)
	}
	e := newAuthRequestEvent(t, "Token "+token)
	e.App = app
	e.Request.Method = http.MethodPost
	if err := requireAuth(e); err != nil {
		t.Fatalf("requireAuth() error = %v", err)
	}
	if err := handleGenerateAuthToken(e); err != nil {
		t.Fatalf("handleGenerateAuthToken() error: %v", err)
	}
	if rec := e.Response.(*httptest.ResponseRecorder); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
	}
	tokens, err := apitoken.List(app, userID, apitoken.ScopePaperless)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("tokens = %v (%v), want only the original", tokens, err)
	}
}

func TestRequireAuthRefusesAnMCPToken(t *testing.T) {
	t.Parallel()

	app := bootSchemaTestApp(t)
	userID := createUser(t, app, "agent@example.com")
	token, err := apitoken.Mint(app, userID, apitoken.ScopeMCP, "agent")
	if err != nil {
		t.Fatal(err)
	}
	e := newAuthRequestEvent(t, "Token "+token)
	e.App = app
	if err := requireAuth(e); !errors.Is(err, errUnauthenticated) {
		t.Fatalf("requireAuth() error = %v, want errUnauthenticated", err)
	}
	assertUnauthorizedDetail(t, e, "Invalid token.")
}

// A superuser has no users account for a Paperless token to belong to.
func TestGenerateAuthTokenRefusesSuperusers(t *testing.T) {
	t.Parallel()

	record := core.NewRecord(core.NewAuthCollection(core.CollectionNameSuperusers))
	record.SetTokenKey("test-token-key-for-superuser")
	e := newAuthRequestEvent(t, "")
	e.Request.Method = http.MethodPost
	e.Auth = record

	if err := handleGenerateAuthToken(e); err != nil {
		t.Fatalf("handleGenerateAuthToken() error: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body %s", rec.Code, rec.Body.String())
	}
}

func TestRequireAuthMissingHeader(t *testing.T) {
	t.Parallel()

	e := newAuthRequestEvent(t, "")
	// The handler must not run after the 401: a nil error let it append its
	// own body to the response.
	if err := requireAuth(e); !errors.Is(err, errUnauthenticated) {
		t.Fatalf("requireAuth() error = %v, want errUnauthenticated", err)
	}
	assertUnauthorizedDetail(t, e, "Authentication credentials were not provided.")
}

func TestRequireAuthInvalidToken(t *testing.T) {
	e := newAuthRequestEvent(t, "Token not-a-jwt")
	e.App = bootTestApp(t)

	// The handler must not run after the 401: a nil error let it append its
	// own body to the response.
	if err := requireAuth(e); !errors.Is(err, errUnauthenticated) {
		t.Fatalf("requireAuth() error = %v, want errUnauthenticated", err)
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
