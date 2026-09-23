package appapi

import (
	"net/http"

	"github.com/pocketbase/pocketbase/core"
)

type userSummary struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

// The users collection's list rule shows a session only itself, so the Settings
// owner picker needs an admin-only view of every account.
func handleListUsers(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		users, err := listUsers(app)
		if err != nil {
			app.Logger().Error("list users failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to list users.")
		}
		return writeJSON(e, http.StatusOK, users)
	}
}

func listUsers(app core.App) ([]userSummary, error) {
	records, err := app.FindRecordsByFilter("users", "", "created", 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]userSummary, 0, len(records))
	for _, record := range records {
		out = append(out, userSummary{ID: record.Id, Email: record.Email(), Name: record.GetString("name")})
	}
	return out, nil
}
