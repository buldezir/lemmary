package worker

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/models"
)

type DetectDuplicatesStep struct{}

func (s *DetectDuplicatesStep) Name() string { return models.StepDetectDuplicates }

func (s *DetectDuplicatesStep) ShouldSkip(state *StepState) (bool, error) {
	ocrText := strings.TrimSpace(state.OCRText)
	if ocrText == "" {
		ocrText = strings.TrimSpace(state.Document.GetString("ocr_text"))
	}
	if ocrText == "" {
		return true, nil
	}
	state.OCRText = ocrText
	return false, nil
}

// ponytail: this step assumes documents are fingerprinted in creation order,
// which is what WORKER_CONCURRENCY=1 gives it. FindNearDuplicate only looks at
// documents created *before* this one, so the pair is caught when the older is
// fingerprinted first and the newer then finds it.
//
// Above 1 that ordering is gone, and a lock does not restore it: if the newer
// document wins the race it scans an older row that has no fingerprint yet and
// finds nothing, and the older one then scans `created <` itself and never
// looks at the newer. Both miss, permanently. Closing it means dropping the
// asymmetric filter and marking whichever of the two is newer -- a change to
// the duplicates package with its own blast radius, worth doing when somebody
// actually needs near-duplicate detection and concurrency together. Exact
// (checksum) duplicate detection runs on upload and is unaffected either way.
func (s *DetectDuplicatesStep) Run(ctx context.Context, state *StepState) error {
	_ = ctx

	ocrText := strings.TrimSpace(state.OCRText)
	if ocrText == "" {
		ocrText = strings.TrimSpace(state.Document.GetString("ocr_text"))
	}
	if ocrText == "" {
		return nil
	}

	fp := duplicates.FingerprintHex(ocrText)
	if fp != "" && state.Document.GetString("text_fingerprint") != fp {
		state.Document.Set("text_fingerprint", fp)
		if err := state.App.Save(state.Document); err != nil {
			return fmt.Errorf("save text fingerprint: %w", err)
		}
	}

	if !state.Cfg.NearDuplicateDetectionEnabled {
		return nil
	}
	if state.Document.GetString("duplicate_of") != "" {
		return nil
	}

	threshold := state.Cfg.NearDuplicateThreshold
	if threshold <= 0 || threshold > 1 {
		threshold = config.DefaultNearDuplicateThreshold
	}

	match, score, err := duplicates.FindNearDuplicate(state.App, state.Document, ocrText, threshold)
	if err != nil {
		return fmt.Errorf("near duplicate search: %w", err)
	}
	if match == nil {
		return nil
	}

	if _, err := duplicates.MarkAsDuplicate(state.App, state.Document, match); err != nil {
		return fmt.Errorf("mark near duplicate: %w", err)
	}
	state.Job.Set("status", models.JobStatusNeedsReview)
	state.Logger.Info("near duplicate detected",
		"duplicate_of", match.Id,
		"score", score,
	)
	return nil
}

// Marking a possible duplicate reviewed keeps it as a document of its own,
// whichever way it is marked (Inbox, document page, metadata save, MCP), and
// also when a reprocess of it failed and its owner edited it by hand: the
// pair is kept apart from then on, and the steps a duplicate skips are queued.
// Extraction only when it has no title yet and nobody wrote its metadata by
// hand, so what an import or the owner gave it is not overwritten.
//
// Refused while a job is open: the run that marked the duplicate sets
// needs_review before it finishes, and finishing saves its own copy of the
// document, link included, over this one.
func keepReviewedDuplicate(e *core.RecordEvent) error {
	record := e.Record
	was := record.Original().GetString("processing_status")
	if record.GetString("duplicate_of") == "" ||
		(was != models.DocStatusNeedsReview && was != models.DocStatusFailed) ||
		record.GetString("processing_status") != models.DocStatusCompleted {
		return e.Next()
	}
	active, err := e.App.CountRecords("processing_jobs", dbx.HashExp{"document": record.Id, "finished_at": ""})
	if err != nil {
		return err
	}
	if active > 0 {
		return router.NewBadRequestError("This document is still being processed. Try again in a moment.", nil)
	}

	duplicates.KeepApart(record)
	record.Set("processing_status", models.DocStatusPending)
	steps := []string{models.StepEmbed, models.StepLinkRelated}
	if strings.TrimSpace(record.GetString("title")) == "" && record.GetString("metadata_source") != models.MetadataSourceUser {
		steps = slices.Clone(models.ExtractionPipelineSteps)
	}
	return e.App.RunInTransaction(func(txApp core.App) error {
		e.App = txApp
		if err := e.Next(); err != nil {
			return err
		}
		_, err := createProcessingJob(txApp, record.Id, steps, nil, config.Overrides{})
		return err
	})
}
