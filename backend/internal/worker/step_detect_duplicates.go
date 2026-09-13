package worker

import (
	"context"
	"fmt"
	"strings"

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
