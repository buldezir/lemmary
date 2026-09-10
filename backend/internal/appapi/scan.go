package appapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/escl"
	"lemmary/backend/internal/limits"
)

// discoverTimeout bounds a discovery request. The sweep and the mDNS browse run
// concurrently inside it and it returns as soon as both are done, so the usual
// /24 still answers in about four seconds; this only has to be large enough
// that the largest range escl accepts -- a /22, sixteen rounds of probes --
// finishes rather than being cut off and reported as "found nothing".
const discoverTimeout = 15 * time.Second

type scanRequest struct {
	Scanner  string `json:"scanner"`
	Source   string `json:"source"`
	UploadID string `json:"upload_id"`
}

type scanSaveRequest struct {
	UploadID string `json:"upload_id"`
}

// handleGetScanDiscover looks for eSCL scanners on the network.
func handleGetScanDiscover(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		cidr := strings.TrimSpace(e.Request.URL.Query().Get("cidr"))
		if cidr == "" {
			// The app is on a container network of its own, so the only hint it
			// has about which LAN to sweep is where the browser came from.
			cidr = escl.DefaultCIDR(e.RealIP())
		}

		ctx, cancel := context.WithTimeout(e.Request.Context(), discoverTimeout)
		defer cancel()

		scanners, err := escl.Discover(ctx, cidr)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		// "It found nothing" is the report that needs debugging, and the two
		// things worth knowing are which range was swept and whether mDNS is
		// reaching this container at all.
		app.Logger().Debug("scanner discovery finished",
			"component", "scan", "cidr", cidr, "found", len(scanners))
		return writeJSON(e, http.StatusOK, map[string]any{
			"scanners": scanners,
			"cidr":     cidr,
		})
	}
}

// handlePostScan starts a scan, appending to a document already being scanned
// when the request names one.
func handlePostScan(app core.App, lim limits.Limits) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req scanRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		source, err := escl.ParseSource(req.Source)
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		// Checked before the scanner is asked to do anything: hearing that the
		// instance is full is worth having before a stack of paper goes through
		// the feeder, not after.
		if exceeded := preflightImport(app, lim, 1, 0, 0); exceeded != nil {
			return writeError(e, http.StatusBadRequest, exceeded.Message)
		}

		jobID, err := escl.Start(app, ownerID, req.Scanner, source, strings.TrimSpace(req.UploadID))
		switch {
		case errors.Is(err, escl.ErrUploadNotFound):
			return writeError(e, http.StatusNotFound, "That scan expired. Start a new one.")
		case errors.Is(err, escl.ErrScanInProgress):
			return writeError(e, http.StatusConflict, "A scan is already in progress.")
		case err != nil:
			return writeError(e, http.StatusBadRequest, err.Error())
		}
		return writeJSON(e, http.StatusAccepted, map[string]any{
			"job_id": jobID,
			"status": escl.JobStatusRunning,
		})
	}
}

// handleGetScanStatus reports on a running scan.
func handleGetScanStatus(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		jobID := strings.TrimSpace(e.Request.URL.Query().Get("job_id"))
		if jobID == "" {
			return writeError(e, http.StatusBadRequest, "job_id is required.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		job, ok := escl.GetJob(jobID)
		if !ok || job.OwnerUserID != ownerID {
			return writeError(e, http.StatusNotFound, "Scan job not found.")
		}
		return writeJSON(e, http.StatusOK, jobPayload(job.ID, job.Status, job.Progress, job.Error, job.Result))
	}
}

// handleGetScanPDF serves the document scanned so far, for the preview.
func handleGetScanPDF(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		uploadID := strings.TrimSpace(e.Request.URL.Query().Get("upload_id"))
		if uploadID == "" {
			return writeError(e, http.StatusBadRequest, "upload_id is required.")
		}
		path, done, ok := escl.Path(uploadID, ownerID)
		if !ok {
			return writeError(e, http.StatusNotFound, "Scan not found or expired.")
		}
		defer done()

		file, err := os.Open(path)
		if err != nil {
			app.Logger().Error("staged scan could not be read", slog.Any("error", err))
			return writeError(e, http.StatusNotFound, "Scan not found or expired.")
		}
		defer file.Close()

		e.Response.Header().Set("Content-Type", "application/pdf")
		// It changes with every page added, and it is somebody's document.
		e.Response.Header().Set("Cache-Control", "private, no-store")
		http.ServeContent(e.Response, e.Request, "scan.pdf", time.Time{}, file)
		return nil
	}
}

// handleDeleteScan throws away a scan the user did not keep.
func handleDeleteScan(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		uploadID := strings.TrimSpace(e.Request.URL.Query().Get("upload_id"))
		if uploadID == "" {
			return writeError(e, http.StatusBadRequest, "upload_id is required.")
		}
		if !escl.Discard(uploadID, ownerID) {
			return writeError(e, http.StatusNotFound, "Scan not found or expired.")
		}
		return writeJSON(e, http.StatusOK, map[string]string{"status": "discarded"})
	}
}

// handlePostScanDocument turns the scanned pages into a document.
func handlePostScanDocument(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		var req scanSaveRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}
		ownerID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		uploadID := strings.TrimSpace(req.UploadID)
		if uploadID == "" {
			return writeError(e, http.StatusBadRequest, "upload_id is required.")
		}

		documentID, err := escl.Save(app, uploadID, ownerID)
		if err != nil {
			return writeScanSaveError(app, e, err)
		}
		return writeJSON(e, http.StatusOK, map[string]string{"document_id": documentID})
	}
}

// writeScanSaveError says the same things the Files tab says, so a duplicate or
// an exhausted allowance reads the same however the document arrived.
func writeScanSaveError(app core.App, e *core.RequestEvent, err error) error {
	var dup *duplicates.ErrDuplicate
	switch {
	case errors.Is(err, escl.ErrUploadNotFound):
		return writeError(e, http.StatusNotFound, "That scan expired. Start a new one.")
	case errors.Is(err, escl.ErrTooLarge):
		return writeError(e, http.StatusBadRequest, err.Error())
	case errors.As(err, &dup):
		return writeJSON(e, http.StatusBadRequest, map[string]any{
			"detail":       "This document is already in your library.",
			"duplicate_of": dup.ExistingID,
		})
	}
	if exceeded := limits.AsExceeded(err); exceeded != nil {
		return writeError(e, http.StatusBadRequest, exceeded.Message)
	}
	app.Logger().Error("saving a scan failed", slog.Any("error", err))
	return writeError(e, http.StatusInternalServerError, "Failed to save the scan.")
}
