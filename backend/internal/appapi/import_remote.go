package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/remoteimport"
)

type importRemoteRequest struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
	Mode   string `json:"mode"`
}

func handlePostImportRemote(app core.App, start func(app core.App, ownerUserID, baseURL, apiKey, mode string) (string, error)) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req importRemoteRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if strings.TrimSpace(req.URL) == "" {
			return writeError(e, http.StatusBadRequest, "URL is required.")
		}
		if strings.TrimSpace(req.APIKey) == "" {
			return writeError(e, http.StatusBadRequest, "API key is required.")
		}
		mode, err := remoteimport.ParseMode(req.Mode)
		if err != nil {
			return writeBadRequest(e, err)
		}

		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		jobID, err := start(app, ownerID, req.URL, req.APIKey, mode)
		if errors.Is(err, remoteimport.ErrImportInProgress) {
			return writeError(e, http.StatusConflict, "An import is already in progress.")
		}
		if err != nil {
			return writeErrorf(e, http.StatusBadRequest, "Import failed to start: %v", err)
		}
		return writeJSON(e, http.StatusAccepted, map[string]any{
			"job_id": jobID,
			"status": remoteimport.JobStatusRunning,
		})
	}
}

func handleGetImportRemoteStatus(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		jobID := strings.TrimSpace(e.Request.URL.Query().Get("job_id"))
		if jobID == "" {
			return writeError(e, http.StatusBadRequest, "job_id is required.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		job, ok := remoteimport.GetJob(jobID)
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
