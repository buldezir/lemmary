package appapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	validation "github.com/pocketbase/ozzo-validation/v4"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

type managedUser struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Admin   bool   `json:"admin"`
	Created string `json:"created"`
}

type managedUserRequest struct {
	Email    *string `json:"email"`
	Name     *string `json:"name"`
	Password *string `json:"password"`
}

var errManagedAdmin = errors.New("admin accounts are not managed here")

func managedUserJSON(record *core.Record) managedUser {
	return managedUser{
		ID:      record.Id,
		Email:   record.Email(),
		Name:    record.GetString("name"),
		Admin:   record.GetBool(pairedAdminField),
		Created: record.GetDateTime("created").String(),
	}
}

func handleListManagedUsers(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		records, err := app.FindRecordsByFilter("users", "", "created", 0, 0)
		if err != nil {
			app.Logger().Error("list managed users failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to list users.")
		}
		out := make([]managedUser, 0, len(records))
		for _, record := range records {
			out = append(out, managedUserJSON(record))
		}
		return writeJSON(e, http.StatusOK, out)
	}
}

func handleCreateManagedUser(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req managedUserRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if req.Email == nil || strings.TrimSpace(*req.Email) == "" {
			return writeError(e, http.StatusBadRequest, "Email is required.")
		}
		if req.Password == nil || *req.Password == "" {
			return writeError(e, http.StatusBadRequest, "Password is required.")
		}
		collection, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return writeError(e, http.StatusInternalServerError, "Failed to create user.")
		}
		record := core.NewRecord(collection)
		record.SetVerified(true)
		applyManagedUserRequest(record, req)
		if err := app.Save(record); err != nil {
			return writeManagedUserSaveError(app, e, err)
		}
		return writeJSON(e, http.StatusCreated, managedUserJSON(record))
	}
}

func handlePatchManagedUser(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := findManagedUser(app, e.Request.PathValue("id"))
		if err != nil {
			return writeManagedUserLookupError(e, err)
		}
		var req managedUserRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if req.Email != nil && strings.TrimSpace(*req.Email) == "" {
			return writeError(e, http.StatusBadRequest, "Email cannot be empty.")
		}
		if req.Password != nil && *req.Password == "" {
			return writeError(e, http.StatusBadRequest, "Password cannot be empty.")
		}
		applyManagedUserRequest(record, req)
		if err := app.Save(record); err != nil {
			return writeManagedUserSaveError(app, e, err)
		}
		return writeJSON(e, http.StatusOK, managedUserJSON(record))
	}
}

func handleDeleteManagedUser(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		record, err := findManagedUser(app, e.Request.PathValue("id"))
		if err != nil {
			return writeManagedUserLookupError(e, err)
		}
		if err := app.Delete(record); err != nil {
			app.Logger().Error("delete managed user failed", "user", record.Id, "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to delete user.")
		}
		e.Response.WriteHeader(http.StatusNoContent)
		return nil
	}
}

func applyManagedUserRequest(record *core.Record, req managedUserRequest) {
	if req.Email != nil {
		record.SetEmail(strings.TrimSpace(*req.Email))
	}
	if req.Name != nil {
		record.Set("name", strings.TrimSpace(*req.Name))
	}
	if req.Password != nil {
		record.SetPassword(*req.Password)
	}
}

func findManagedUser(app core.App, id string) (*core.Record, error) {
	record, err := app.FindRecordById("users", strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if record.GetBool(pairedAdminField) {
		return nil, errManagedAdmin
	}
	return record, nil
}

func writeManagedUserLookupError(e *core.RequestEvent, err error) error {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return writeError(e, http.StatusNotFound, "User not found.")
	case errors.Is(err, errManagedAdmin):
		return writeError(e, http.StatusForbidden, "Admin accounts cannot be changed here.")
	}
	return writeError(e, http.StatusInternalServerError, "Failed to load user.")
}

// The limits hook refuses an over-allowance account with an ApiError, and
// record validation (duplicate email, short password) with validation.Errors.
func writeManagedUserSaveError(app core.App, e *core.RequestEvent, err error) error {
	var apiErr *router.ApiError
	if errors.As(err, &apiErr) {
		return writeError(e, apiErr.Status, apiErr.Message)
	}
	var invalid validation.Errors
	if errors.As(err, &invalid) {
		return writeError(e, http.StatusBadRequest, invalid.Error())
	}
	app.Logger().Error("save managed user failed", "error", err)
	return writeError(e, http.StatusInternalServerError, "Failed to save user.")
}
