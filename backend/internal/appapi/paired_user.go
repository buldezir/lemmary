package appapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// UpsertPairedUser creates or updates the paired admin users-collection record
// for a superuser email so the SPA can own documents via a users session.
//
// Existing non-admin users with the same email are not overwritten.
func UpsertPairedUser(app core.App, email, password string) (*core.Record, error) {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if password == "" {
		return nil, fmt.Errorf("password is required")
	}

	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return nil, err
	}

	record, err := app.FindAuthRecordByEmail("users", email)
	switch {
	case err == nil:
		if !record.GetBool(pairedAdminField) {
			return nil, fmt.Errorf("a non-admin user already exists for %q", email)
		}
	case errors.Is(err, sql.ErrNoRows):
		record = core.NewRecord(collection)
		record.SetEmail(email)
	default:
		return nil, err
	}

	record.SetPassword(password)
	record.SetVerified(true)
	record.Set(pairedAdminField, true)
	if err := app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

// RevokePairedAdmin clears the paired-admin flag for the users account with the
// given email (used when deleting a superuser). The users record is kept so
// owned documents remain accessible.
func RevokePairedAdmin(app core.App, email string) error {
	email = strings.TrimSpace(email)
	if email == "" {
		return nil
	}
	record, err := app.FindAuthRecordByEmail("users", email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if !record.GetBool(pairedAdminField) {
		return nil
	}
	record.Set(pairedAdminField, false)
	return app.Save(record)
}

type ensureUserRequest struct {
	Password string `json:"password"`
}

// handlePostEnsureUser lets a true superuser session create/update the paired
// users account (legacy installs that only have _superusers).
func handlePostEnsureUser(app core.App) func(*core.RequestEvent) error {
	return bindSuperuser(func(e *core.RequestEvent) error {
		var req ensureUserRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if req.Password == "" {
			return writeError(e, http.StatusBadRequest, "Password is required.")
		}
		if len(req.Password) < 8 {
			return writeError(e, http.StatusBadRequest, "Password must be at least 8 characters.")
		}

		email := strings.TrimSpace(e.Auth.Email())
		if email == "" || email == core.DefaultInstallerEmail {
			return writeError(e, http.StatusBadRequest, "Invalid email address.")
		}

		record, err := UpsertPairedUser(app, email, req.Password)
		if err != nil {
			return writeError(e, http.StatusBadRequest, "Failed to ensure user account: "+err.Error())
		}

		return writeJSON(e, http.StatusOK, map[string]string{
			"email": email,
			"id":    record.Id,
		})
	})
}
