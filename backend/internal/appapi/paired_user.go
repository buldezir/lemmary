package appapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// UpsertPairedUser creates or updates a users-collection record with the given
// email and password so a superuser can own documents via a users session.
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
	if err != nil {
		record = core.NewRecord(collection)
		record.SetEmail(email)
	}
	record.SetPassword(password)
	record.SetVerified(true)
	if err := app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
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
