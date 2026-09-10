package appapi

import (
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/zipimport"
)

type importZipRequest struct {
	UploadID string `json:"upload_id"`
}

// handlePostImportUpload stages an uploaded zip so the user can confirm what it
// holds before any document is created.
//
// The source is the only thing the two flows disagree about, and only here: the
// staged upload remembers it, so discard, confirm and status below are shared
// verbatim and their upload and job ids are unique across both.
func handlePostImportUpload(app core.App, lim limits.Limits, src zipimport.Source) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		// Streamed rather than buffered: these archives run to hundreds of MB.
		reader, err := e.Request.MultipartReader()
		if err != nil {
			return writeError(e, http.StatusBadRequest, "Expected a multipart upload.")
		}
		part, fileName, err := archivePart(reader)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		defer part.Close()

		preview, err := zipimport.Inspect(app, src, ownerID, fileName, part)
		if err != nil {
			if detail := archiveErrorDetail(src, err); detail != "" {
				return writeError(e, http.StatusBadRequest, detail)
			}
			app.Logger().Error("archive inspect failed", "source", src, "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to read the archive.")
		}

		// Checked while the user is still deciding whether to confirm, rather
		// than leaving the create hook to refuse the overflow one file at a time
		// partway through the import.
		var bytes int64
		for _, entry := range preview.Files {
			if entry.Duplicate || entry.Oversized {
				continue
			}
			bytes += entry.Size
		}
		if exceeded := preflightImport(app, lim, int64(preview.ImportableCount), 0, bytes); exceeded != nil {
			zipimport.Discard(preview.UploadID, ownerID)
			return writeError(e, http.StatusBadRequest, exceeded.Message)
		}

		return writeJSON(e, http.StatusOK, preview)
	}
}

// handleDeleteImportUpload drops a staged archive the user did not confirm.
func handleDeleteImportUpload(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		uploadID := strings.TrimSpace(e.Request.URL.Query().Get("upload_id"))
		if uploadID == "" {
			return writeError(e, http.StatusBadRequest, "upload_id is required.")
		}
		if !zipimport.Discard(uploadID, ownerID) {
			return writeError(e, http.StatusNotFound, "Upload not found or expired.")
		}
		return writeJSON(e, http.StatusOK, map[string]string{"status": "discarded"})
	}
}

// handlePostImport starts the confirmed import of a staged archive.
func handlePostImport(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req importZipRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		if strings.TrimSpace(req.UploadID) == "" {
			return writeError(e, http.StatusBadRequest, "upload_id is required.")
		}

		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		jobID, err := zipimport.Start(app, ownerID, req.UploadID)
		switch {
		case errors.Is(err, zipimport.ErrUploadNotFound):
			return writeError(e, http.StatusNotFound, "Upload not found or expired. Upload the archive again.")
		case errors.Is(err, zipimport.ErrImportInProgress):
			return writeError(e, http.StatusConflict, "An import is already in progress.")
		case err != nil:
			return writeError(e, http.StatusBadRequest, "Import failed to start: "+err.Error())
		}
		return writeJSON(e, http.StatusAccepted, map[string]any{
			"job_id": jobID,
			"status": zipimport.JobStatusRunning,
		})
	}
}

func handleGetImportStatus(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		jobID := strings.TrimSpace(e.Request.URL.Query().Get("job_id"))
		if jobID == "" {
			return writeError(e, http.StatusBadRequest, "job_id is required.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		job, ok := zipimport.GetJob(jobID)
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

// archiveErrorDetail maps a rejected archive to a client-facing message,
// or "" when the failure is not the caller's fault.
//
// Only the empty-archive case needs the source: "no PDF files" is the useful
// thing to tell someone who uploaded the wrong Amazon export, and misleading to
// someone who uploaded a zip of photos.
func archiveErrorDetail(src zipimport.Source, err error) string {
	noun := "importable files"
	if src == zipimport.SourceAmazon {
		noun = "PDF files"
	}
	switch {
	case errors.Is(err, zipimport.ErrNotArchive):
		return "The upload is not a readable zip archive."
	case errors.Is(err, zipimport.ErrNoFiles):
		return "No " + noun + " found in the archive."
	case errors.Is(err, zipimport.ErrTooManyFiles):
		return "The archive holds too many " + noun + " to import at once."
	case errors.Is(err, zipimport.ErrArchiveTooLarge):
		return "The archive is too large."
	default:
		return ""
	}
}

// archivePart returns the "file" part of a multipart upload without buffering it.
func archivePart(reader *multipart.Reader) (*multipart.Part, string, error) {
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, "", errors.New("File is required.")
		}
		if err != nil {
			return nil, "", errors.New("Invalid multipart form.")
		}
		if part.FormName() == "file" {
			return part, part.FileName(), nil
		}
		part.Close()
	}
}
