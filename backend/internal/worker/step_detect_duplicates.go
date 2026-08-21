package worker

import (
	"context"
	"fmt"
	"strings"

	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/duplicates"
	"paperless-go/backend/internal/models"
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
