package apitoken_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/apitoken"
	"lemmary/backend/internal/testpb"
	_ "lemmary/backend/migrations"
)

func signIn(app core.App, authorization, scope string) (*core.RequestEvent, bool) {
	e := &core.RequestEvent{App: app}
	e.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	e.Request.Header.Set("Authorization", authorization)
	return e, apitoken.Authenticate(e, scope)
}

func TestTokenSignsInOnlyForItsScopeAndOutlivesAPasswordChange(t *testing.T) {
	app := testpb.Open(t)
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(users)
	user.Set("email", "agent@example.com")
	user.SetPassword("first-password-123")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}

	token, err := apitoken.Mint(app, user.Id, apitoken.ScopeMCP, "agent")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.FindAuthRecordByToken(token, core.TokenTypeAuth); err == nil {
		t.Fatal("an API token is accepted as a PocketBase session")
	}

	e, ok := signIn(app, "Bearer "+token, apitoken.ScopeMCP)
	if !ok || e.Auth.Id != user.Id || apitoken.Scope(e) != apitoken.ScopeMCP {
		t.Fatalf("signed in = %v as %v with scope %q", ok, e.Auth, apitoken.Scope(e))
	}
	if _, ok := signIn(app, "Token "+token, apitoken.ScopePaperless); ok {
		t.Fatal("an MCP token signed in to the Paperless scope")
	}
	if _, ok := signIn(app, "Bearer "+token+"x", apitoken.ScopeMCP); ok {
		t.Fatal("an unknown token signed in")
	}

	user.SetPassword("second-password-456")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	if _, ok := signIn(app, "Bearer "+token, apitoken.ScopeMCP); !ok {
		t.Fatal("a password change revoked the token; revoking is per token")
	}
}
