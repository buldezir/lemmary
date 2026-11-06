package appapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/apitoken"
	"lemmary/backend/internal/authguard"
	"lemmary/backend/internal/config"
)

func serveTokenRoutes(t *testing.T, app core.App) http.Handler {
	t.Helper()
	r, err := apis.NewRouter(app)
	if err != nil {
		t.Fatalf("router: %v", err)
	}
	authguard.Register(app)
	RegisterMCP(app, config.NewRuntime(config.AIEnv{}), nil)
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		g := e.Router.Group("/api/app")
		g.GET("/admin/users", bindAdmin(handleListManagedUsers(app)))
		registerAPITokenRoutes(g, app)
		return e.Next()
	})
	if err := app.OnServe().Trigger(&core.ServeEvent{App: app, Router: r}); err != nil {
		t.Fatalf("serve hooks: %v", err)
	}
	mux, err := r.BuildMux()
	if err != nil {
		t.Fatalf("build mux: %v", err)
	}
	return mux
}

func callAs(mux http.Handler, method, path, authorization, body string) (int, string) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

const mcpToolsList = `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`

// GHSA-7wx6-3578-7w2h: an agent token was a ten-year session JWT, so the
// admin's created users and reset passwords. It must open /api/mcp and nothing
// else, and stop when revoked.
func TestMCPTokenOpensOnlyTheMCPEndpoint(t *testing.T) {
	app := bootQueueApp(t)
	admin, err := app.FindRecordById("users", makeQueueUser(t, app, "admin@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	admin.Set(pairedAdminField, true)
	if err := app.Save(admin); err != nil {
		t.Fatal(err)
	}
	session, err := admin.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	mux := serveTokenRoutes(t, app)

	status, body := callAs(mux, http.MethodPost, "/api/app/tokens", session, `{"scope":"mcp","name":"Claude Code"}`)
	if status != http.StatusOK {
		t.Fatalf("mint = %d: %s", status, body)
	}
	var minted struct{ Token string }
	if err := json.Unmarshal([]byte(body), &minted); err != nil || minted.Token == "" {
		t.Fatalf("mint body %s: %v", body, err)
	}
	bearer := "Bearer " + minted.Token

	if status, body := callAs(mux, http.MethodGet, "/api/app/admin/users", session, ""); status != http.StatusOK {
		t.Fatalf("the admin session itself was refused: %d %s", status, body)
	}
	for _, c := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/app/admin/users", ""},
		{http.MethodGet, "/api/collections/documents/records", ""},
		{http.MethodPost, "/api/app/tokens", `{"scope":"mcp"}`},
		{http.MethodGet, "/api/app/tokens?scope=mcp", ""},
	} {
		if status, body := callAs(mux, c.method, c.path, bearer, c.body); status != http.StatusUnauthorized {
			t.Errorf("%s %s with the agent token = %d, want 401: %s", c.method, c.path, status, body)
		}
	}
	if status, body := callAs(mux, http.MethodPost, "/api/mcp", bearer, mcpToolsList); status != http.StatusOK {
		t.Fatalf("agent token on /api/mcp = %d: %s", status, body)
	}

	status, body = callAs(mux, http.MethodGet, "/api/app/tokens?scope=mcp", session, "")
	var listed struct{ Tokens []apitoken.Info }
	if err := json.Unmarshal([]byte(body), &listed); status != http.StatusOK || err != nil || len(listed.Tokens) != 1 {
		t.Fatalf("list = %d %s (%v)", status, body, err)
	}
	if listed.Tokens[0].Name != "Claude Code" || listed.Tokens[0].LastUsed == "" {
		t.Fatalf("listed %+v, want the name and a last use", listed.Tokens[0])
	}
	if status, body := callAs(mux, http.MethodDelete, "/api/app/tokens/"+listed.Tokens[0].ID, session, ""); status != http.StatusNoContent {
		t.Fatalf("revoke = %d: %s", status, body)
	}
	if status, _ := callAs(mux, http.MethodPost, "/api/mcp", bearer, mcpToolsList); status != http.StatusUnauthorized {
		t.Fatalf("revoked token on /api/mcp = %d, want 401", status)
	}
}

func TestMCPRefusesAPaperlessToken(t *testing.T) {
	app := bootQueueApp(t)
	userID := makeQueueUser(t, app, "reader@example.com")
	token, err := apitoken.Mint(app, userID, apitoken.ScopePaperless, "Paperparrot")
	if err != nil {
		t.Fatal(err)
	}
	mux := serveTokenRoutes(t, app)
	if status, body := callAs(mux, http.MethodPost, "/api/mcp", "Bearer "+token, mcpToolsList); status != http.StatusUnauthorized {
		t.Fatalf("paperless token on /api/mcp = %d, want 401: %s", status, body)
	}
}

func TestOnlyTheOwnerRevokesAToken(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other, err := app.FindRecordById("users", makeQueueUser(t, app, "other@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := apitoken.Mint(app, owner, apitoken.ScopeMCP, ""); err != nil {
		t.Fatal(err)
	}
	tokens, err := apitoken.List(app, owner, apitoken.ScopeMCP)
	if err != nil || len(tokens) != 1 {
		t.Fatalf("list: %v %v", tokens, err)
	}
	session, err := other.NewAuthToken()
	if err != nil {
		t.Fatal(err)
	}
	mux := serveTokenRoutes(t, app)
	if status, body := callAs(mux, http.MethodDelete, "/api/app/tokens/"+tokens[0].ID, session, ""); status != http.StatusNotFound {
		t.Fatalf("revoke by another account = %d, want 404: %s", status, body)
	}
}
