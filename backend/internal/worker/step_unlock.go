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

// The check passes only when the file opened, or when PDFium itself rejected
// it as damaged, which is left for preview to report as before this step. Any
// other failure -- a worker that did not start, a timeout -- says nothing
// about the password, so it fails the step for a retry rather than letting the
// steps after it read a file that may be locked.
func (s *UnlockStep) Run(ctx context.Context, state *StepState) error {
	if err := ensureTempFile(state); err != nil {
		return err
	}
	_, err := pdftool.PageCount(ctx, state.TmpPath)
	if err == nil || errors.Is(err, pdftool.ErrUnreadable) {
		return nil
	}
	return err
}
