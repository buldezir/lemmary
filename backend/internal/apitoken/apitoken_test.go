package apitoken_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
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

func TestTokensAreNumberedPastTheHighestInUse(t *testing.T) {
	app := testpb.Open(t)
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(users)
	user.Set("email", "numbers@example.com")
	user.SetPassword("numbers-password-123")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	mint := func(scope, base string) {
		t.Helper()
		if _, err := apitoken.Mint(app, user.Id, scope, base); err != nil {
			t.Fatal(err)
		}
	}
	names := func(scope string) []string {
		t.Helper()
		tokens, err := apitoken.List(app, user.Id, scope)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, token := range tokens {
			out = append(out, token.Name)
		}
		slices.Sort(out)
		return out
	}

	mint(apitoken.ScopePaperless, "")
	mint(apitoken.ScopePaperless, "")
	mint(apitoken.ScopeMCP, "Claude Code MCP")
	mint(apitoken.ScopeMCP, "Claude Code MCP")
	mint(apitoken.ScopeMCP, "Cursor MCP")
	if got, want := names(apitoken.ScopePaperless), []string{"ngx API token #1", "ngx API token #2"}; !slices.Equal(got, want) {
		t.Fatalf("paperless names = %v, want %v", got, want)
	}
	if got, want := names(apitoken.ScopeMCP), []string{"Claude Code MCP #1", "Claude Code MCP #2", "Cursor MCP #1"}; !slices.Equal(got, want) {
		t.Fatalf("mcp names = %v, want %v", got, want)
	}

	tokens, err := apitoken.List(app, user.Id, apitoken.ScopePaperless)
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range tokens {
		if token.Name == "ngx API token #1" {
			if err := apitoken.Revoke(app, user.Id, token.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	mint(apitoken.ScopePaperless, "")
	if got, want := names(apitoken.ScopePaperless), []string{"ngx API token #2", "ngx API token #3"}; !slices.Equal(got, want) {
		t.Fatalf("after a revoke, paperless names = %v, want %v", got, want)
	}
}
