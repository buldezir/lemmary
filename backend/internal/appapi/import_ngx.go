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
	return func(e *core.RequestEvent) error {
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
			return writeImportOwnerError(e, err)
		}

		jobID, err := ngximport.Start(app, ownerID, req.URL, req.APIKey, mode)
		if errors.Is(err, ngximport.ErrImportInProgress) {
			return writeError(e, http.StatusConflict, "An import is already in progress.")
		}
		if err != nil {
			return writeError(e, http.StatusBadRequest, "Import failed to start: "+err.Error())
		}
		return writeJSON(e, http.StatusAccepted, map[string]any{
			"job_id": jobID,
			"status": ngximport.JobStatusRunning,
		})
	}
}

func handleGetImportNgxStatus(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		jobID := strings.TrimSpace(e.Request.URL.Query().Get("job_id"))
		if jobID == "" {
			return writeError(e, http.StatusBadRequest, "job_id is required.")
		}
		ownerID, err := resolveImportOwnerID(app, e)
		if err != nil {
			return writeImportOwnerError(e, err)
		}
		job, ok := ngximport.GetJob(jobID)
		if !ok || job.OwnerUserID != ownerID {
			return writeError(e, http.StatusNotFound, "Import job not found.")
		}
		payload := map[string]any{
			"job_id": job.ID,
			"status": job.Status,
		}
		if job.Error != "" {
			payload["error"] = job.Error
		}
		if job.Result != nil {
			payload["result"] = job.Result
		}
		return writeJSON(e, http.StatusOK, payload)
	}
}

type importOwnerClientError struct {
	msg string
}

func (e *importOwnerClientError) Error() string {
	return e.msg
}

func writeImportOwnerError(e *core.RequestEvent, err error) error {
	var clientErr *importOwnerClientError
	if errors.As(err, &clientErr) {
		return writeError(e, http.StatusBadRequest, clientErr.Error())
	}
	return writeError(e, http.StatusInternalServerError, "Failed to resolve import owner.")
}

func resolveImportOwnerID(app core.App, e *core.RequestEvent) (string, error) {
	if e.Auth == nil {
		return "", &importOwnerClientError{msg: "Authentication required."}
	}
	if e.Auth.Collection().Name == "users" {
		return e.Auth.Id, nil
	}
	email := strings.TrimSpace(e.Auth.Email())
	if email == "" {
		return "", &importOwnerClientError{msg: "Admin account has no email; cannot resolve document owner."}
	}
	user, err := app.FindAuthRecordByEmail("users", email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", &importOwnerClientError{msg: "No paired users account for this admin; sign in with the admin user account."}
		}
		return "", err
	}
	return user.Id, nil
}
