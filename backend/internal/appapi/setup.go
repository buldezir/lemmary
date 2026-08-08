package appapi

import (
	"encoding/json"
	"net/http"
	"net/mail"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/config"
)

type setupStatusResponse struct {
	NeedsAdmin            bool   `json:"needs_admin"`
	NeedsConfig           bool   `json:"needs_config"`
	OCRProvider           string `json:"ocr_provider"`
	GoogleVisionAPIKeySet bool   `json:"google_vision_api_key_set"`
	MistralAPIKeySet      bool   `json:"mistral_api_key_set"`
	OpenAIAPIKeySet       bool   `json:"openai_api_key_set"`
}

type setupAdminRequest struct {
	Email           string `json:"email"`
	Password        string `json:"password"`
	PasswordConfirm string `json:"passwordConfirm"`
}

func handleGetSetupStatus(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		if err := config.EnsureDefaults(app); err != nil {
			app.Logger().Warn("ensure settings before setup status failed", "error", err)
		} else {
			_ = rt.Reload(app)
		}

		cfg := rt.Snapshot().Cfg
		needsAdmin, err := needsAdminSetup(app)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to check setup status.")
		}

		return writeJSON(e, http.StatusOK, setupStatusResponse{
			NeedsAdmin:            needsAdmin,
			NeedsConfig:           needsConfigSetup(cfg),
			OCRProvider:           cfg.OCRProvider,
			GoogleVisionAPIKeySet: cfg.GoogleVisionAPIKey != "",
			MistralAPIKeySet:      cfg.MistralAPIKey != "",
			OpenAIAPIKeySet:       cfg.OpenAIAPIKey != "",
		})
	}
}

func handlePostSetupAdmin(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		needsAdmin, err := needsAdminSetup(app)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to check setup status.")
		}
		if !needsAdmin {
			return writeError(e, http.StatusConflict, "An admin account already exists.")
		}

		var req setupAdminRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}

		email := strings.TrimSpace(req.Email)
		if email == "" {
			return writeError(e, http.StatusBadRequest, "Email is required.")
		}
		if _, err := mail.ParseAddress(email); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid email address.")
		}
		if email == core.DefaultInstallerEmail {
			return writeError(e, http.StatusBadRequest, "Invalid email address.")
		}
		if req.Password == "" {
			return writeError(e, http.StatusBadRequest, "Password is required.")
		}
		if req.Password != req.PasswordConfirm {
			return writeError(e, http.StatusBadRequest, "Passwords do not match.")
		}
		if len(req.Password) < 8 {
			return writeError(e, http.StatusBadRequest, "Password must be at least 8 characters.")
		}

		collection, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to create admin account.")
		}

		record := core.NewRecord(collection)
		record.SetEmail(email)
		record.SetPassword(req.Password)
		record.SetVerified(true)

		if err := app.Save(record); err != nil {
			return writeError(e, http.StatusBadRequest, "Failed to create admin account: "+err.Error())
		}

		userRecord, err := UpsertPairedUser(app, email, req.Password)
		if err != nil {
			_ = app.Delete(record)
			return writeError(e, http.StatusBadRequest, "Failed to create paired user account: "+err.Error())
		}

		return writeJSON(e, http.StatusCreated, map[string]string{
			"email":   email,
			"id":      record.Id,
			"user_id": userRecord.Id,
		})
	}
}

// needsAdminSetup is true when no real superuser exists (excluding PocketBase's installer account).
func needsAdminSetup(app core.App) (bool, error) {
	total, err := app.CountRecords(core.CollectionNameSuperusers, dbx.Not(dbx.HashExp{
		"email": core.DefaultInstallerEmail,
	}))
	if err != nil {
		return false, err
	}
	return total == 0, nil
}

func needsConfigSetup(cfg config.Config) bool {
	if strings.TrimSpace(cfg.OpenAIAPIKey) == "" {
		return true
	}
	switch strings.TrimSpace(cfg.OCRProvider) {
	case "mistral":
		return strings.TrimSpace(cfg.MistralAPIKey) == ""
	default:
		return strings.TrimSpace(cfg.GoogleVisionAPIKey) == ""
	}
}
