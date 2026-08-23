package amazonimport

import (
	"archive/zip"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"paperless-go/backend/internal/duplicates"
	"paperless-go/backend/internal/importjob"
	"paperless-go/backend/internal/models"
)

// maxReportedErrors bounds the error list returned to the client.
const maxReportedErrors = 25

// Job statuses for in-memory async imports.
const (
	JobStatusRunning   = importjob.StatusRunning
	JobStatusCompleted = importjob.StatusCompleted
	JobStatusFailed    = importjob.StatusFailed
)

// ErrImportInProgress is returned when another import is already running.
var ErrImportInProgress = importjob.ErrBusy

// ErrUploadNotFound is returned when the upload id is unknown, expired, or
// belongs to someone else.
var ErrUploadNotFound = errors.New("upload not found or expired")

// Result summarizes a completed import run.
type Result struct {
	Imported          int      `json:"imported"`
	SkippedDuplicates int      `json:"skipped_duplicates"`
	SkippedOversized  int      `json:"skipped_oversized"`
	Failed            int      `json:"failed"`
	Errors            []string `json:"errors"`
}

// Job is an in-memory import run snapshot (lost on process restart).
type Job = importjob.Job[Result]

var registry = importjob.NewRegistry[Result](importjob.DefaultRetention)

// Start imports the PDFs of a staged archive in the background and returns the
// job id. The archive is consumed: the upload id is no longer valid afterwards.
// Only one import may run at a time per owner.
func Start(app core.App, ownerUserID, uploadID string) (string, error) {
	item, ok := claimStaged(strings.TrimSpace(uploadID), ownerUserID)
	if !ok {
		return "", ErrUploadNotFound
	}

	jobID, err := registry.Start(ownerUserID, func(report func(done, total int)) (Result, error) {
		defer releaseStaged(item)
		return runImport(app, ownerUserID, item, report)
	})
	if err != nil {
		// The job never started, so put the archive back for another attempt.
		restoreStaged(item)
		return "", err
	}
	return jobID, nil
}

// GetJob returns a copy of the in-memory job, or false if unknown.
func GetJob(id string) (Job, bool) {
	return registry.Get(id)
}

func runImport(app core.App, ownerUserID string, item *stagedArchive, report func(done, total int)) (Result, error) {
	result := Result{Errors: []string{}}

	zr, err := zip.OpenReader(item.path)
	if err != nil {
		return result, ErrNotArchive
	}
	defer zr.Close()

	byPath := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byPath[f.Name] = f
	}

	entries := item.preview.Files
	total := len(entries)
	report(0, total)

	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return result, err
	}

	for i, entry := range entries {
		applyEntry(app, collection, ownerUserID, entry, byPath[entry.Path], &result)
		report(i+1, total)
	}

	return result, nil
}

// applyEntry imports one previewed entry and folds the outcome into result.
func applyEntry(app core.App, collection *core.Collection, ownerUserID string, entry Entry, file *zip.File, result *Result) {
	switch {
	case entry.Oversized:
		result.SkippedOversized++
		return
	case entry.Duplicate:
		// Already in the library, or repeated inside this archive.
		result.SkippedDuplicates++
		return
	}
	if file == nil {
		result.Failed++
		appendError(result, fmt.Sprintf("%s: missing from archive", entry.Path))
		return
	}
	if err := importOnePDF(app, collection, ownerUserID, entry, file); err != nil {
		var dup *duplicates.ErrDuplicate
		if errors.As(err, &dup) {
			result.SkippedDuplicates++
			return
		}
		result.Failed++
		appendError(result, fmt.Sprintf("%s: %v", entry.Path, err))
		return
	}
	result.Imported++
}

func importOnePDF(app core.App, collection *core.Collection, ownerUserID string, entry Entry, file *zip.File) error {
	data, err := readEntry(file)
	if err != nil {
		return fmt.Errorf("read from archive: %w", err)
	}

	fsFile, err := filesystem.NewFileFromBytes(data, entry.Name)
	if err != nil {
		return fmt.Errorf("prepare file: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("user", ownerUserID)
	record.Set("file", fsFile)
	record.Set("processing_status", models.DocStatusPending)

	if err := app.Save(record); err != nil {
		var dup *duplicates.ErrDuplicate
		if errors.As(err, &dup) {
			return dup
		}
		if dup := duplicates.ErrDuplicateFromAPIError(err); dup != nil {
			return dup
		}
		if dup := duplicates.ErrDuplicateFromSaveConflict(app, record, err); dup != nil {
			return dup
		}
		return err
	}
	return nil
}

func appendError(result *Result, msg string) {
	if len(result.Errors) >= maxReportedErrors {
		return
	}
	result.Errors = append(result.Errors, msg)
}
