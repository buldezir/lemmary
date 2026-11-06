package appapi

import (
	"errors"
	"net/http"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
	"lemmary/backend/internal/apitoken"
)

// registerAPITokenRoutes takes a session: bindAuth never admits an API token,
// so one cannot mint, list or revoke others.
func registerAPITokenRoutes(g *router.RouterGroup[*core.RequestEvent], app core.App) {
	g.GET("/tokens", bindAuth(handleListAPITokens(app)))
	g.POST("/tokens", bindAuth(handleCreateAPIToken(app)))
	g.DELETE("/tokens/{id}", bindAuth(handleRevokeAPIToken(app)))
}

type createAPITokenRequest struct {
	Scope string `json:"scope"`
	Name  string `json:"name"`
}

func handleListAPITokens(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		userID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		scope := e.Request.URL.Query().Get("scope")
		if !apitoken.ValidScope(scope) {
			return writeError(e, http.StatusBadRequest, "Unknown token scope.")
		}
		tokens, err := apitoken.List(app, userID, scope)
		if err != nil {
			app.Logger().Error("api token list failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to list tokens.")
		}
		return writeJSON(e, http.StatusOK, map[string]any{"tokens": tokens})
	}
}

func handleCreateAPIToken(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req createAPITokenRequest
		if err := e.BindBody(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if !apitoken.ValidScope(req.Scope) {
			return writeError(e, http.StatusBadRequest, "Unknown token scope.")
		}
		userID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		token, err := apitoken.Mint(app, userID, req.Scope, req.Name)
		if err != nil {
			app.Logger().Error("api token mint failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to create the token.")
		}
		return writeJSON(e, http.StatusOK, map[string]any{"token": token})
	}
}

func handleRevokeAPIToken(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		userID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		err = apitoken.Revoke(app, userID, e.Request.PathValue("id"))
		switch {
		case errors.Is(err, apitoken.ErrNotFound):
			return writeError(e, http.StatusNotFound, "Token not found.")
		case err != nil:
			app.Logger().Error("api token revoke failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to revoke the token.")
		}
		e.Response.WriteHeader(http.StatusNoContent)
		return nil
	}
}
