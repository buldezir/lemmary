package worker

import (
	"context"
	"fmt"
	"strings"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/embed"
	"lemmary/backend/internal/models"
)

// Every failure here is soft: without vectors the archive is still searchable
// by keyword, while failing the job would buy a perfectly good document a retry
// that re-runs OCR and extraction to fix an embedding request.
type EmbedStep struct {
	Embedder ai.Embedder
}

func (s *EmbedStep) Name() string { return models.StepEmbed }

func (s *EmbedStep) ShouldSkip(state *StepState) (bool, error) {
	if s.Embedder == nil {
		// The feature is off, and off must look like a skipped step.
		return true, nil
	}
	if state.Document == nil || state.Document.GetString("duplicate_of") != "" {
		return true, nil
	}
	if strings.TrimSpace(state.Document.GetString("ocr_text")) == "" {
		return true, nil
	}
	return false, nil
}

func (s *EmbedStep) Run(ctx context.Context, state *StepState) error {
	if s.Embedder == nil {
		return nil
	}

	result, err := embed.EmbedDocument(ctx, state.App, s.Embedder, state.Document, state.forced(models.StepEmbed), state.Logger)
	if err != nil {
		// Wrapped, so the run is recorded as failed and the backfill cron picks
		// the document up again after its backoff.
		return fmt.Errorf("%w: %w", ErrStepSoft, err)
	}
	if result.Skipped {
		state.Logger.Info("embeddings already current", "document", state.Document.Id)
		return nil
	}

	state.Logger.Info("document embedded",
		"document", state.Document.Id,
		"chunks", result.Chunks,
		"dims", result.Dims,
		"prompt_tokens", result.PromptTokens,
		"requests", result.Requests,
		"truncated", result.Truncated,
	)

	// The vector length is only knowable from a real response.
	if err := config.RecordEmbeddingDims(state.App, result.Dims); err != nil {
		state.Logger.Warn("recording embedding dimensions failed", "error", err)
	}
	return nil
}
