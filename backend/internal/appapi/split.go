package appapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/importjob"
	"paperless-go/backend/internal/pdfsplit"
)

type splitRequest struct {
	UploadID string          `json:"upload_id"`
	Parts    []pdfsplit.Part `json:"parts"`
}

type splitDetectRequest struct {
	UploadID string `json:"upload_id"`
}

// handlePostSplitUpload stages a multi-document PDF so the user can mark the
// cuts before any document is created.
func handlePostSplitUpload(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}

		// Streamed rather than buffered: a long colour scan runs to tens of MB.
		reader, err := e.Request.MultipartReader()
		if err != nil {
			return writeError(e, http.StatusBadRequest, "Expected a multipart upload.")
		}
		part, fileName, err := archivePart(reader)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		defer part.Close()

		preview, err := pdfsplit.Inspect(app, ownerID, fileName, part)
		if err != nil {
			if detail := splitUploadErrorDetail(err); detail != "" {
				return writeError(e, http.StatusBadRequest, detail)
			}
			app.Logger().Error("split upload staging failed", slog.Any("error", err))
			return writeError(e, http.StatusInternalServerError, "Failed to read the PDF.")
		}
		return writeJSON(e, http.StatusOK, preview)
	}
}

// handleDeleteSplitUpload drops a staged PDF the user did not split.
func handleDeleteSplitUpload(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		uploadID := strings.TrimSpace(e.Request.URL.Query().Get("upload_id"))
		if uploadID == "" {
			return writeError(e, http.StatusBadRequest, "upload_id is required.")
		}
		if !pdfsplit.Discard(uploadID, ownerID) {
			return writeError(e, http.StatusNotFound, "Upload not found or expired.")
		}
		return writeJSON(e, http.StatusOK, map[string]string{"status": "discarded"})
	}
}

// handleGetSplitPage serves the cached thumbnail of one page of a staged PDF.
func handleGetSplitPage(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		query := e.Request.URL.Query()
		uploadID := strings.TrimSpace(query.Get("upload_id"))
		page, convErr := strconv.Atoi(strings.TrimSpace(query.Get("page")))
		if uploadID == "" || convErr != nil {
			return writeError(e, http.StatusBadRequest, "upload_id and page are required.")
		}

		// Rendered on first request rather than at upload time, so this can be
		// the slow call for a page nobody has looked at yet.
		data, err := pdfsplit.PageThumb(uploadID, ownerID, page)
		if err != nil {
			if !errors.Is(err, pdfsplit.ErrUploadNotFound) {
				app.Logger().Error("split page thumbnail failed", slog.Any("error", err))
			}
			return writeError(e, http.StatusNotFound, "Page not found.")
		}

		e.Response.Header().Set("Content-Type", "image/png")
		// Thumbnails never change and the upload expires anyway, so let the
		// browser keep them for the life of the staged upload.
		e.Response.Header().Set("Cache-Control", "private, max-age=1800")
		e.Response.WriteHeader(http.StatusOK)
		_, writeErr := e.Response.Write(data)
		return writeErr
	}
}

// handlePostSplitDetect starts an AI pass proposing where the PDF should be cut.
func handlePostSplitDetect(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req splitDetectRequest
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

		snap := rt.Snapshot()
		jobID, err := pdfsplit.Detect(app, ownerID, req.UploadID, pdfsplit.DetectDeps{
			Splitter:   snap.Splitter,
			OCR:        snap.OCR,
			OCRTimeout: snap.Cfg.OCRTimeout,
			LLMTimeout: snap.Cfg.OpenAITimeout,
		})
		switch {
		case errors.Is(err, pdfsplit.ErrUploadNotFound):
			return writeError(e, http.StatusNotFound, "Upload not found or expired. Upload the PDF again.")
		case errors.Is(err, pdfsplit.ErrDetectUnavailable):
			return writeError(e, http.StatusBadRequest, "Automatic detection needs an extraction model; configure one in Settings.")
		case errors.Is(err, pdfsplit.ErrDetectInProgress):
			return writeError(e, http.StatusConflict, "A detection is already in progress.")
		case err != nil:
			return writeError(e, http.StatusBadRequest, "Detection failed to start: "+err.Error())
		}
		return writeJSON(e, http.StatusAccepted, map[string]any{
			"job_id": jobID,
			"status": pdfsplit.JobStatusRunning,
		})
	}
}

func handleGetSplitDetectStatus(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		jobID := strings.TrimSpace(e.Request.URL.Query().Get("job_id"))
		if jobID == "" {
			return writeError(e, http.StatusBadRequest, "job_id is required.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		job, ok := pdfsplit.GetDetectJob(jobID)
		if !ok || job.OwnerUserID != ownerID {
			return writeError(e, http.StatusNotFound, "Detection job not found.")
		}
		return writeJSON(e, http.StatusOK, jobPayload(job.ID, job.Status, job.Progress, job.Error, job.Result))
	}
}

// handlePostSplit starts the confirmed split of a staged PDF.
func handlePostSplit(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req splitRequest
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

		jobID, err := pdfsplit.Start(app, ownerID, req.UploadID, req.Parts)
		switch {
		case errors.Is(err, pdfsplit.ErrUploadNotFound):
			return writeError(e, http.StatusNotFound, "Upload not found or expired. Upload the PDF again.")
		case errors.Is(err, pdfsplit.ErrSplitInProgress):
			return writeError(e, http.StatusConflict, "A split is already in progress.")
		case err != nil:
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		return writeJSON(e, http.StatusAccepted, map[string]any{
			"job_id": jobID,
			"status": pdfsplit.JobStatusRunning,
		})
	}
}

func handleGetSplitStatus(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		jobID := strings.TrimSpace(e.Request.URL.Query().Get("job_id"))
		if jobID == "" {
			return writeError(e, http.StatusBadRequest, "job_id is required.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		job, ok := pdfsplit.GetJob(jobID)
		if !ok || job.OwnerUserID != ownerID {
			return writeError(e, http.StatusNotFound, "Split job not found.")
		}
		return writeJSON(e, http.StatusOK, jobPayload(job.ID, job.Status, job.Progress, job.Error, job.Result))
	}
}

// jobPayload renders an in-memory job snapshot the way the polling client
// expects it, omitting the fields that are only set once the run is over.
func jobPayload[T any](id, status string, progress importjob.Progress, jobErr string, result *T) map[string]any {
	payload := map[string]any{
		"job_id":   id,
		"status":   status,
		"progress": progress,
	}
	if jobErr != "" {
		payload["error"] = jobErr
	}
	if result != nil {
		payload["result"] = result
	}
	return payload
}

// splitUploadErrorDetail maps a rejected upload to a client-facing message,
// or "" when the failure is not the caller's fault.
func splitUploadErrorDetail(err error) string {
	switch {
	case errors.Is(err, pdfsplit.ErrNotPDF):
		return "The upload is not a readable PDF."
	case errors.Is(err, pdfsplit.ErrPDFTooLarge):
		return "The PDF is too large."
	case errors.Is(err, pdfsplit.ErrSinglePage):
		return "This PDF has only one page, so there is nothing to split. Use the Files tab to upload it."
	case errors.Is(err, pdfsplit.ErrTooManyPages):
		return "The PDF has too many pages to split at once."
	default:
		return ""
	}
}
