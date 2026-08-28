package appapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/archiveimport"
)

type importArchiveRequest struct {
	UploadID string `json:"upload_id"`
	Mode     string `json:"mode"`
}

// handlePostImportArchiveUpload stages a Lemmary backup archive so the user can
// see what it holds before any document is created.
func handlePostImportArchiveUpload(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		// Streamed rather than buffered: a backup of a whole library runs to
		// hundreds of MB.
		reader, err := e.Request.MultipartReader()
		if err != nil {
			return writeError(e, http.StatusBadRequest, "Expected a multipart upload.")
		}
		part, fileName, err := archivePart(reader)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		defer part.Close()

		preview, err := archiveimport.Inspect(app, ownerID, fileName, part)
		if err != nil {
			if detail := backupArchiveErrorDetail(err); detail != "" {
				return writeError(e, http.StatusBadRequest, detail)
			}
			app.Logger().Error("lemmary archive inspect failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to read the archive.")
		}
		return writeJSON(e, http.StatusOK, preview)
	}
}

// handleDeleteImportArchiveUpload drops a staged archive the user did not confirm.
func handleDeleteImportArchiveUpload(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		uploadID := strings.TrimSpace(e.Request.URL.Query().Get("upload_id"))
		if uploadID == "" {
			return writeError(e, http.StatusBadRequest, "upload_id is required.")
		}
		if !archiveimport.Discard(uploadID, ownerID) {
			return writeError(e, http.StatusNotFound, "Upload not found or expired.")
		}
		return writeJSON(e, http.StatusOK, map[string]string{"status": "discarded"})
	}
}

// handlePostImportArchive starts the confirmed restore of a staged archive.
func handlePostImportArchive(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req importArchiveRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if strings.TrimSpace(req.UploadID) == "" {
			return writeError(e, http.StatusBadRequest, "upload_id is required.")
		}
		if _, err := archiveimport.ParseMode(req.Mode); err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		jobID, err := archiveimport.Start(app, ownerID, req.UploadID, req.Mode)
		switch {
		case errors.Is(err, archiveimport.ErrUploadNotFound):
			return writeError(e, http.StatusNotFound, "Upload not found or expired. Upload the archive again.")
		case errors.Is(err, archiveimport.ErrImportInProgress):
			return writeError(e, http.StatusConflict, "An import is already in progress.")
		case err != nil:
			return writeError(e, http.StatusBadRequest, "Import failed to start: "+err.Error())
		}
		return writeJSON(e, http.StatusAccepted, map[string]any{
			"job_id": jobID,
			"status": archiveimport.JobStatusRunning,
		})
	}
}

func handleGetImportArchiveStatus(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		jobID := strings.TrimSpace(e.Request.URL.Query().Get("job_id"))
		if jobID == "" {
			return writeError(e, http.StatusBadRequest, "job_id is required.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		job, ok := archiveimport.GetJob(jobID)
		if !ok || job.OwnerUserID != ownerID {
			return writeError(e, http.StatusNotFound, "Import job not found.")
		}
		payload := map[string]any{
			"job_id":   job.ID,
			"status":   job.Status,
			"progress": job.Progress,
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

// backupArchiveErrorDetail maps a rejected archive to a client-facing message,
// or "" when the failure is not the caller's fault.
func backupArchiveErrorDetail(err error) string {
	switch {
	case errors.Is(err, archiveimport.ErrNotArchive):
		return "The upload is not a readable Lemmary export archive."
	case errors.Is(err, archiveimport.ErrNoDocuments):
		return "No documents found in the archive."
	case errors.Is(err, archiveimport.ErrTooManyDocuments):
		return "The archive holds too many documents to import at once."
	case errors.Is(err, archiveimport.ErrArchiveTooLarge):
		return "The archive is too large."
	case errors.Is(err, archiveimport.ErrArchiveTooDense):
		return "The archive decompresses beyond the allowed size."
	case errors.Is(err, archiveimport.ErrUnsupportedVersion):
		return "The archive was created by a newer version of Lemmary."
	default:
		return ""
	}
}
