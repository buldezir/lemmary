package appapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/ngximport"
)

type importNgxRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
	Mode   string `json:"mode"`
}

func handlePostImportNgx(app core.App) func(*core.RequestEvent) error {
	return bindAdmin(func(e *core.RequestEvent) error {
		var req importNgxRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if strings.TrimSpace(req.URL) == "" {
			return writeError(e, http.StatusBadRequest, "URL is required.")
		}
		if strings.TrimSpace(req.APIKey) == "" {
			return writeError(e, http.StatusBadRequest, "API key is required.")
		}
		mode, err := ngximport.ParseMode(req.Mode)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		ownerID, err := resolveImportOwnerID(app, e)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		result, err := ngximport.Run(app, ownerID, req.URL, req.APIKey, mode)
		if errors.Is(err, ngximport.ErrImportInProgress) {
			return writeError(e, http.StatusConflict, "An import is already in progress.")
		}
		if err != nil {
			return writeError(e, http.StatusBadRequest, "Import failed: "+err.Error())
		}
		return writeJSON(e, http.StatusOK, result)
	})
}

func resolveImportOwnerID(app core.App, e *core.RequestEvent) (string, error) {
	if e.Auth == nil {
		return "", errors.New("Authentication required.")
	}
	if e.Auth.Collection().Name == "users" {
		return e.Auth.Id, nil
	}
	email := strings.TrimSpace(e.Auth.Email())
	if email == "" {
		return "", errors.New("Admin account has no email; cannot resolve document owner.")
	}
	user, err := app.FindAuthRecordByEmail("users", email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", errors.New("No paired users account for this admin; sign in with the admin user account.")
		}
		return "", err
	}
	return user.Id, nil
}
