package amazonimport

import (
	"archive/zip"
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/importjob"
	"lemmary/backend/internal/models"
)

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
// job id. A run that gets as far as importing something consumes the archive:
// the upload id is no longer valid afterwards.
// Only one import may run at a time per owner.
func Start(app core.App, ownerUserID, uploadID string) (string, error) {
	item, ok := stagingRegistry.Claim(uploadID, ownerUserID)
	if !ok {
		return "", ErrUploadNotFound
	}

	jobID, err := registry.Start(ownerUserID, func(report func(done, total int)) (result Result, runErr error) {
		finished := false
		// Settled from a defer because a panic in the run unwinds straight past
		// here into the job registry's recover, and an archive nobody settles is
		// neither retryable nor sweepable.
		defer func() { settleUpload(item, result, runErr, finished) }()
		result, runErr = runImport(app, ownerUserID, item, report)
		finished = true
		return result, runErr
	})
	if err != nil {
		// The job never started, so put the archive back for another attempt.
		stagingRegistry.Restore(item)
		return "", err
	}
	return jobID, nil
}

// settleUpload ends the hold an import job took on the staged archive. A run
// that failed before importing anything — a corrupt zip, a missing collection —
// leaves it staged so it can be retried without a re-upload.
func settleUpload(item *stagedArchive, result Result, runErr error, finished bool) {
	if !finished || (runErr != nil && result.Imported == 0) {
		stagingRegistry.Restore(item)
		return
	}
	stagingRegistry.Release(item)
}

// GetJob returns a copy of the in-memory job, or false if unknown.
func GetJob(id string) (Job, bool) {
	return registry.Get(id)
}

func runImport(app core.App, ownerUserID string, item *stagedArchive, report func(done, total int)) (Result, error) {
	result := Result{Errors: []string{}}

	zr, err := zip.OpenReader(item.Path)
	if err != nil {
		return result, ErrNotArchive
	}
	defer zr.Close()

	// Match preview entries to zip entries by position, not by name: duplicate
	// entry names are legal in a zip, and a name-keyed map would import the
	// last file's bytes for every same-named entry. The preview was built by
	// walking the same staged file with the same filter, so order aligns.
	pdfFiles := make([]*zip.File, 0, len(zr.File))
	for _, f := range zr.File {
		if isPDFEntry(f) {
			pdfFiles = append(pdfFiles, f)
		}
	}

	entries := item.Payload.Files
	total := len(entries)
	report(0, total)

	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return result, err
	}

	for i, entry := range entries {
		var file *zip.File
		if i < len(pdfFiles) && pdfFiles[i].Name == entry.Path {
			file = pdfFiles[i]
		}
		applyEntry(app, collection, ownerUserID, entry, file, &result)
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
		result.Errors = importjob.AppendError(result.Errors, fmt.Sprintf("%s: missing from archive", entry.Path))
		return
	}
	if err := importOnePDF(app, collection, ownerUserID, entry, file); err != nil {
		var dup *duplicates.ErrDuplicate
		if errors.As(err, &dup) {
			result.SkippedDuplicates++
			return
		}
		result.Failed++
		result.Errors = importjob.AppendError(result.Errors, fmt.Sprintf("%s: %v", entry.Path, err))
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

	return duplicates.NormalizeSaveError(app, record, app.Save(record))
}
