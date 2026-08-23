// Package reprocess requeues documents whose processing failed.
//
// A failed job cannot be reopened: processing_jobs has no update rule, and its
// step_runs carry exhausted attempt counters. So requeueing means creating a
// fresh pending job, exactly as the single-document reprocess form does.
package reprocess

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"paperless-go/backend/internal/models"
	"paperless-go/backend/internal/worker"
)

const (
	// DefaultLimit is how many documents one batch queues when the caller does
	// not say. Small enough that a mistaken click is cheap to absorb.
	DefaultLimit = 100
	// MaxLimit caps a single batch. The worker drains serially, so a larger
	// batch does not finish sooner - it only commits more AI spend up front.
	MaxLimit = 1000
)

// Mode decides which pipeline steps a requeued document runs.
type Mode string

const (
	// ModeAuto re-runs only what the document still needs: extraction alone
	// when OCR text survived the failed run, the full pipeline otherwise.
	ModeAuto Mode = "auto"
	// ModeFull re-runs every step, including OCR.
	ModeFull Mode = "full"
	// ModeExtraction re-runs metadata extraction over the existing OCR text.
	ModeExtraction Mode = "extraction"
)

// ParseMode validates a mode from a request body. An empty value means ModeAuto.
func ParseMode(raw string) (Mode, error) {
	switch mode := Mode(strings.TrimSpace(raw)); mode {
	case "":
		return ModeAuto, nil
	case ModeAuto, ModeFull, ModeExtraction:
		return mode, nil
	default:
		return "", fmt.Errorf("unknown reprocess mode %q", raw)
	}
}

// Request describes one batch.
type Request struct {
	OwnerUserID string
	// DocumentIDs restricts the batch to these documents (ownership is still
	// enforced). When set, Limit is ignored - the caller already picked the set.
	DocumentIDs []string
	Limit       int
	Mode        Mode
}

// Result reports what a batch did. Remaining counts documents still failed
// afterwards, so a caller knows whether another batch is worth running.
type Result struct {
	Queued    int `json:"queued"`
	Skipped   int `json:"skipped"`
	Remaining int `json:"remaining"`
}

// StepsFor returns the pipeline steps to run for document, and the subset to
// force. apply_metadata is never forced: forcing it would overwrite metadata the
// user corrected by hand.
func StepsFor(document *core.Record, mode Mode) (steps []string, forceSteps []string) {
	switch mode {
	case ModeFull:
		steps = models.FullPipelineSteps
	case ModeExtraction:
		steps = models.ExtractionPipelineSteps
	default:
		if document != nil && strings.TrimSpace(document.GetString("ocr_text")) != "" {
			steps = models.ExtractionPipelineSteps
		} else {
			steps = models.FullPipelineSteps
		}
	}

	steps = slices.Clone(steps)
	forceSteps = make([]string, 0, len(steps))
	for _, step := range steps {
		if step != models.StepApplyMetadata {
			forceSteps = append(forceSteps, step)
		}
	}
	return steps, forceSteps
}

// RunBatch queues a fresh job for up to Limit of the owner's failed documents.
func RunBatch(app core.App, req Request) (Result, error) {
	if strings.TrimSpace(req.OwnerUserID) == "" {
		return Result{}, fmt.Errorf("owner user id is required")
	}
	mode, err := ParseMode(string(req.Mode))
	if err != nil {
		return Result{}, err
	}

	documents, skipped, err := selectDocuments(app, req)
	if err != nil {
		return Result{}, err
	}

	result := Result{Skipped: skipped}
	for _, document := range documents {
		steps, forceSteps := StepsFor(document, mode)
		if err := queueOne(app, document, steps, forceSteps); err != nil {
			return result, fmt.Errorf("queue document %s: %w", document.Id, err)
		}
		result.Queued++
	}

	remaining, err := failedCount(app, req.OwnerUserID)
	if err != nil {
		return result, err
	}
	result.Remaining = remaining

	app.Logger().Info("reprocess batch finished",
		slog.String("owner", req.OwnerUserID),
		slog.String("mode", string(mode)),
		slog.Int("queued", result.Queued),
		slog.Int("skipped", result.Skipped),
		slog.Int("remaining", result.Remaining),
	)
	return result, nil
}

// queueOne flips the document back to pending and creates its job in one
// transaction. Order matters: the job's after-create hook kicks the worker,
// which would set the document to processing, so the job must not become visible
// before the pending write lands. A rollback also means a failed create cannot
// leave the document stranded at pending with no job to move it.
func queueOne(app core.App, document *core.Record, steps, forceSteps []string) error {
	return app.RunInTransaction(func(txApp core.App) error {
		document.Set("processing_status", models.DocStatusPending)
		if err := txApp.Save(document); err != nil {
			return err
		}
		_, err := worker.Enqueue(txApp, document.Id, steps, forceSteps)
		return err
	})
}

// selectDocuments resolves the batch, returning the documents to queue and how
// many candidates were passed over.
func selectDocuments(app core.App, req Request) ([]*core.Record, int, error) {
	if len(req.DocumentIDs) > 0 {
		return selectByID(app, req)
	}

	documents, err := app.FindRecordsByFilter(
		"documents",
		"user = {:user} && processing_status = {:status}",
		"created",
		clampLimit(req.Limit),
		0,
		map[string]any{"user": req.OwnerUserID, "status": models.DocStatusFailed},
	)
	if err != nil {
		return nil, 0, err
	}
	return documents, 0, nil
}

// clampLimit keeps one batch inside [1, MaxLimit], treating a missing limit as
// DefaultLimit.
func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultLimit
	}
	if limit > MaxLimit {
		return MaxLimit
	}
	return limit
}

// selectByID loads an explicit selection, dropping anything the caller does not
// own or that is already queued. A selection made in the UI can go stale while
// it sits on screen, so this is what stops a double-queue.
func selectByID(app core.App, req Request) ([]*core.Record, int, error) {
	documents := make([]*core.Record, 0, len(req.DocumentIDs))
	skipped := 0
	seen := make(map[string]bool, len(req.DocumentIDs))

	for _, id := range req.DocumentIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		document, err := app.FindRecordById("documents", id)
		if err != nil {
			// Deleted between selection and submit: not an error worth failing
			// the whole batch over.
			skipped++
			continue
		}
		if document.GetString("user") != req.OwnerUserID {
			skipped++
			continue
		}
		switch document.GetString("processing_status") {
		case models.DocStatusPending, models.DocStatusProcessing:
			skipped++
			continue
		}
		documents = append(documents, document)
	}
	return documents, skipped, nil
}

func failedCount(app core.App, ownerUserID string) (int, error) {
	total, err := app.CountRecords(
		"documents",
		dbx.HashExp{"user": ownerUserID, "processing_status": models.DocStatusFailed},
	)
	if err != nil {
		return 0, err
	}
	return int(total), nil
}
