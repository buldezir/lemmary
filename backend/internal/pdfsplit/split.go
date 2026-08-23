package pdfsplit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/importjob"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/pdftool"
	"lemmary/backend/internal/strutil"
)

// maxPartNameBytes keeps a generated part file name well inside what the
// storage layer and the UI can carry. The sanitized stem is ASCII, so a byte
// cap is also a character cap.
const maxPartNameBytes = 80

// Job statuses for in-memory async splits.
const (
	JobStatusRunning   = importjob.StatusRunning
	JobStatusCompleted = importjob.StatusCompleted
	JobStatusFailed    = importjob.StatusFailed
)

// ErrSplitInProgress is returned when another split is already running.
var ErrSplitInProgress = importjob.ErrBusy

// Part is one requested sub-document, as inclusive 1-based page numbers.
type Part struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// Result summarizes a completed split run.
type Result struct {
	Created           int      `json:"created"`
	SkippedDuplicates int      `json:"skipped_duplicates"`
	SkippedOversized  int      `json:"skipped_oversized"`
	Failed            int      `json:"failed"`
	Errors            []string `json:"errors"`
	DocumentIDs       []string `json:"document_ids"`
}

// Job is an in-memory split run snapshot (lost on process restart).
type Job = importjob.Job[Result]

var registry = importjob.NewRegistry[Result](importjob.DefaultRetention)

// ValidateParts requires the parts to cover every page exactly once, in order.
//
// That is precisely what the cut-marking UI can express, so keeping the
// contract exact lets a malformed request be rejected with a concrete message
// instead of silently producing documents the user did not ask for.
func ValidateParts(parts []Part, pageCount int) error {
	if len(parts) == 0 {
		return fmt.Errorf("at least one part is required")
	}
	if len(parts) > pageCount {
		return fmt.Errorf("%d parts cannot fit in %d pages", len(parts), pageCount)
	}

	next := 1
	for i, part := range parts {
		if part.From < 1 || part.To < part.From || part.To > pageCount {
			return fmt.Errorf("part %d has an invalid page range %d-%d", i+1, part.From, part.To)
		}
		if part.From != next {
			return fmt.Errorf("part %d starts at page %d, expected page %d: parts must cover every page exactly once, in order", i+1, part.From, next)
		}
		next = part.To + 1
	}
	if next != pageCount+1 {
		return fmt.Errorf("parts end at page %d, expected page %d: parts must cover every page exactly once", next-1, pageCount)
	}
	return nil
}

// Start splits a staged PDF in the background and returns the job id. A run
// that gets as far as creating documents consumes the upload: the upload id is
// no longer valid afterwards, and the original PDF is discarded rather than kept
// as a document of its own. Only one split may run at a time per owner.
func Start(app core.App, ownerUserID, uploadID string, parts []Part) (string, error) {
	item, ok := stagingRegistry.Claim(uploadID, ownerUserID)
	if !ok {
		return "", ErrUploadNotFound
	}

	if err := ValidateParts(parts, item.Payload.preview.PageCount); err != nil {
		// Nothing was consumed, so the upload stays available for a fixed request.
		stagingRegistry.Restore(item)
		return "", err
	}

	jobID, err := registry.Start(ownerUserID, func(report func(done, total int)) (result Result, runErr error) {
		finished := false
		// Settled from a defer because a panic in the run unwinds straight past
		// here into the job registry's recover, and an upload nobody settles is
		// neither retryable nor sweepable.
		defer func() { settleUpload(item, result, runErr, finished) }()
		result, runErr = runSplit(app, ownerUserID, item, parts, report)
		finished = true
		return result, runErr
	})
	if err != nil {
		// The job never started, so put the upload back for another attempt.
		stagingRegistry.Restore(item)
		return "", err
	}
	return jobID, nil
}

// settleUpload ends the hold a split job took on the staged upload.
//
// The upload is consumed once the run got as far as creating documents (or
// finished cleanly). A run that fell over before a single document existed
// leaves it staged instead: retrying beats making the user upload the scan and
// mark every cut again.
func settleUpload(item *stagedPDF, result Result, runErr error, finished bool) {
	if !finished || (runErr != nil && result.Created == 0) {
		stagingRegistry.Restore(item)
		return
	}
	stagingRegistry.Release(item)
}

// GetJob returns a copy of the in-memory job, or false if unknown.
func GetJob(id string) (Job, bool) {
	return registry.Get(id)
}

func runSplit(app core.App, ownerUserID string, item *stagedPDF, parts []Part, report func(done, total int)) (Result, error) {
	result := Result{Errors: []string{}, DocumentIDs: []string{}}

	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return result, err
	}

	workDir, err := os.MkdirTemp("", "lemmary-split-*")
	if err != nil {
		return result, fmt.Errorf("prepare work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	baseName := partBaseName(item.Payload.preview.FileName)
	total := len(parts)
	report(0, total)

	for i, part := range parts {
		name := partFileName(baseName, part)
		if err := applyPart(app, collection, ownerUserID, sourcePathOf(item), workDir, name, part, &result); err != nil {
			result.Errors = importjob.AppendError(result.Errors, fmt.Sprintf("pages %d-%d: %v", part.From, part.To, err))
		}
		report(i+1, total)
	}

	app.Logger().Info("pdf split finished",
		"component", "pdf_split",
		"upload_id", item.ID,
		"parts", total,
		"created", result.Created,
		"duplicates", result.SkippedDuplicates,
		"oversized", result.SkippedOversized,
		"failed", result.Failed,
	)
	return result, nil
}

// applyPart extracts one page range and saves it as a document, folding the
// outcome into result. A returned error is the caller's to report; the counters
// are already updated.
func applyPart(
	app core.App,
	collection *core.Collection,
	ownerUserID, sourcePath, workDir, name string,
	part Part,
	result *Result,
) error {
	partPath := filepath.Join(workDir, name)
	if err := pdftool.ExtractRange(context.Background(), sourcePath, part.From, part.To, partPath); err != nil {
		result.Failed++
		return fmt.Errorf("extract pages: %w", err)
	}
	defer os.Remove(partPath)

	info, err := os.Stat(partPath)
	if err != nil {
		result.Failed++
		return fmt.Errorf("read extracted part: %w", err)
	}
	if info.Size() > maxPartBytes {
		result.SkippedOversized++
		return nil
	}

	data, err := os.ReadFile(partPath)
	if err != nil {
		result.Failed++
		return fmt.Errorf("read extracted part: %w", err)
	}
	fsFile, err := filesystem.NewFileFromBytes(data, name)
	if err != nil {
		result.Failed++
		return fmt.Errorf("prepare file: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("user", ownerUserID)
	record.Set("file", fsFile)
	record.Set("processing_status", models.DocStatusPending)

	if err := duplicates.NormalizeSaveError(app, record, app.Save(record)); err != nil {
		var dup *duplicates.ErrDuplicate
		if errors.As(err, &dup) {
			// Already in the library — re-splitting the same scan is a normal
			// thing to do, so it is not a failure.
			result.SkippedDuplicates++
			return nil
		}
		result.Failed++
		return err
	}

	result.Created++
	result.DocumentIDs = append(result.DocumentIDs, record.Id)
	return nil
}

// partBaseName derives a safe file-name stem from the uploaded file name.
func partBaseName(fileName string) string {
	base := filepath.Base(strings.TrimSpace(fileName))
	base = strings.TrimSuffix(base, filepath.Ext(base))

	var b strings.Builder
	lastDash := false
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteRune('-')
				lastDash = true
			}
		}
	}

	cleaned := strings.Trim(strutil.Truncate(b.String(), maxPartNameBytes), "-.")
	if cleaned == "" {
		return "document"
	}
	return cleaned
}

// partFileName names a part after the pages it holds, so the origin of a
// document stays readable in the library.
func partFileName(baseName string, part Part) string {
	if part.From == part.To {
		return fmt.Sprintf("%s-page-%d.pdf", baseName, part.From)
	}
	return fmt.Sprintf("%s-pages-%d-%d.pdf", baseName, part.From, part.To)
}
