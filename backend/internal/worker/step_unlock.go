package worker

import (
	"context"
	"errors"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/pdftool"
)

// UnlockStep only detects: the password never reaches a job, so the decryption
// itself happens in the unlock endpoint, which then queues a fresh pipeline.
type UnlockStep struct{}

func (s *UnlockStep) Name() string { return models.StepUnlock }

func (s *UnlockStep) ShouldSkip(state *StepState) (bool, error) {
	return state.MimeType != "application/pdf", nil
}

// Any other pdfinfo failure is left for preview to report, as before this step.
func (s *UnlockStep) Run(ctx context.Context, state *StepState) error {
	if err := ensureTempFile(state); err != nil {
		return err
	}
	if _, err := pdftool.PageCount(ctx, state.TmpPath); errors.Is(err, pdftool.ErrPasswordRequired) {
		return err
	}
	return nil
}
