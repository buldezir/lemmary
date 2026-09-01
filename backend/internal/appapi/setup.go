package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

var (
	errAdminExists  = errors.New("an admin account already exists")
	errInvalidAdmin = errors.New("invalid admin credentials")
)

// invalidAdmin is a rejection of what somebody typed, carrying the sentence to
// show them and nothing else.
//
// Wrapping with %w around errInvalidAdmin would put "invalid admin credentials:
// " in front of every one of these, and the setup wizard renders the message
// verbatim — so the first thing a new admin saw was "invalid admin credentials:
// Email is required.". Is() keeps errors.Is working for the caller that has to
// tell bad input from a database failure.
type invalidAdmin struct{ msg string }

func (e invalidAdmin) Error() string        { return e.msg }
func (e invalidAdmin) Is(target error) bool { return target == errInvalidAdmin }

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
		if err := config.EnsureDefaults(app, rt.Env()); err != nil {
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
		var req setupAdminRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if req.Password != req.PasswordConfirm {
			return writeError(e, http.StatusBadRequest, "Passwords do not match.")
		}

		email := strings.TrimSpace(req.Email)
		superuserID, userID, err := CreateFirstAdmin(app, email, req.Password)
		switch {
		case errors.Is(err, errAdminExists):
			return writeError(e, http.StatusConflict, "An admin account already exists.")
		case errors.Is(err, errInvalidAdmin):
			return writeError(e, http.StatusBadRequest, err.Error())
		case err != nil:
			app.Logger().Error("setup admin creation failed", "error", err)
			return writeError(e, http.StatusBadRequest, "Failed to create admin account.")
		}

		return writeJSON(e, http.StatusCreated, map[string]string{
			"email":   email,
			"id":      superuserID,
			"user_id": userID,
		})
	}
}

// CreateFirstAdmin mints the instance's first admin: a _superusers record and
// the paired users account that owns documents, with the same credentials.
//
// Shared by the setup wizard and the SETUP_ADMIN_* bootstrap so the two cannot
// drift — the pairing is the part that matters, and an admin who exists as a
// superuser but not as a user cannot own a document they upload.
//
// Refuses when an admin already exists. Both callers depend on that: the wizard
// endpoint is unauthenticated by design, and the bootstrap runs on every boot.
func CreateFirstAdmin(app core.App, email, password string) (superuserID, userID string, err error) {
	email = strings.TrimSpace(email)
	if err := validateAdminCredentials(email, password); err != nil {
		return "", "", err
	}

	needsAdmin, err := needsAdminSetup(app)
	if err != nil {
		return "", "", err
	}
	if !needsAdmin {
		return "", "", errAdminExists
	}

	collection, err := app.FindCollectionByNameOrId(core.CollectionNameSuperusers)
	if err != nil {
		return "", "", err
	}

	record := core.NewRecord(collection)
	record.SetEmail(email)
	record.SetPassword(password)
	record.SetVerified(true)

	// The needs-admin check and the create must be atomic: the wizard endpoint
	// is unauthenticated, so two racing requests would otherwise both pass the
	// guard above and both mint a superuser.
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
		userRecord, err = UpsertPairedUser(txApp, email, password)
		return err
	})
	if err != nil {
		return "", "", err
	}
	return record.Id, userRecord.Id, nil
}

// validateAdminCredentials reports every rejection as an invalidAdmin, so a
// caller can tell "this input is wrong" — which an HTTP handler reports verbatim
// and the boot-time bootstrap logs — from a database failure, which neither
// should show to anyone.
func validateAdminCredentials(email, password string) error {
	switch {
	case email == "":
		return invalidAdmin{"Email is required."}
	case email == core.DefaultInstallerEmail:
		return invalidAdmin{"Invalid email address."}
	case password == "":
		return invalidAdmin{"Password is required."}
	case len(password) < 8:
		return invalidAdmin{"Password must be at least 8 characters."}
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return invalidAdmin{"Invalid email address."}
	}
	return nil
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
