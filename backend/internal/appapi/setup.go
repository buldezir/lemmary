package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/aiprovider"
	"paperless-go/backend/internal/config"
)

var errAdminExists = errors.New("an admin account already exists")

type setupStatusResponse struct {
	NeedsAdmin    bool `json:"needs_admin"`
	NeedsConfig   bool `json:"needs_config"`
	HasOCR        bool `json:"has_ocr"`
	HasLLM        bool `json:"has_llm"`
	ProviderCount int  `json:"provider_count"`
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
		}
		// No reload here: this endpoint is polled on every page load and the
		// runtime is already kept current by the settings/provider record hooks.

		cfg := rt.Snapshot().Cfg
		needsAdmin, err := needsAdminSetup(app)
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to check setup status.")
		}

		providerCount := 0
		if providers, listErr := aiprovider.List(app); listErr == nil {
			providerCount = len(providers)
		}

		return writeJSON(e, http.StatusOK, setupStatusResponse{
			NeedsAdmin:    needsAdmin,
			NeedsConfig:   needsConfigSetup(cfg),
			HasOCR:        config.HasOCR(cfg),
			HasLLM:        config.HasLLM(cfg),
			ProviderCount: providerCount,
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

		// This endpoint is unauthenticated by design, so the needs-admin check
		// and the create must be atomic: two racing requests would otherwise
		// both pass the guard at the top and both mint a superuser.
		var userRecord *core.Record
		err = app.RunInTransaction(func(txApp core.App) error {
			stillNeeded, err := needsAdminSetup(txApp)
			if err != nil {
				return err
			}
			if !stillNeeded {
				return errAdminExists
			}
			if err := txApp.Save(record); err != nil {
				return err
			}
			userRecord, err = UpsertPairedUser(txApp, email, req.Password)
			return err
		})
		if err != nil {
			if errors.Is(err, errAdminExists) {
				return writeError(e, http.StatusConflict, "An admin account already exists.")
			}
			app.Logger().Error("setup admin creation failed", "error", err)
			return writeError(e, http.StatusBadRequest, "Failed to create admin account.")
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
	return !config.HasOCR(cfg) || !config.HasLLM(cfg)
}
