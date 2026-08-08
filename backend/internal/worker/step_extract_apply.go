package worker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"paperless-go/backend/internal/ai"
	"paperless-go/backend/internal/models"
)

type ExtractMetadataStep struct {
	Extractor ai.Extractor
}

func (s *ExtractMetadataStep) Name() string { return models.StepExtractMetadata }

func (s *ExtractMetadataStep) ShouldSkip(state *StepState) (bool, error) {
	if state.Document != nil && state.Document.GetString("duplicate_of") != "" {
		return true, nil
	}
	if state.forced(models.StepExtractMetadata) {
		return false, nil
	}
	if state.Metadata.Populated() {
		return true, nil
	}
	if metadata, err := loadMetadataJSON(state.Job); err != nil {
		return false, err
	} else if metadata.Populated() {
		state.Metadata = metadata
		return true, nil
	}
	return false, nil
}

func (s *ExtractMetadataStep) Run(ctx context.Context, state *StepState) error {
	ocrText := strings.TrimSpace(state.OCRText)
	if ocrText == "" {
		ocrText = strings.TrimSpace(state.Document.GetString("ocr_text"))
	}
	if ocrText == "" {
		return fmt.Errorf("extract_metadata requires ocr_text")
	}
	state.OCRText = ocrText

	state.Logger.Info("starting AI extraction",
		"provider", s.Extractor.Name(),
		"model", s.Extractor.Model(),
		"ocr_chars", len(ocrText),
	)

	aiStart := time.Now()
	aiCtx, cancel := context.WithTimeout(ctx, state.Cfg.OpenAITimeout)
	defer cancel()

	metadata, err := s.Extractor.ExtractMetadata(aiCtx, ocrText)
	if err != nil {
		state.Logger.Error("AI extraction failed",
			"duration", time.Since(aiStart).Round(time.Millisecond),
			slog.Any("error", err),
		)
		return fmt.Errorf("ai extraction: %w", err)
	}

	state.Logger.Info("AI extraction complete",
		"duration", time.Since(aiStart).Round(time.Millisecond),
		"confidence", metadata.Confidence,
		"title", truncateForLog(metadata.Title, 80),
		"type", truncateForLog(metadata.DocumentType, 40),
		"tags", len(metadata.Tags),
	)

	state.Metadata = metadata
	saveMetadataJSON(state.Job, metadata)
	if err := state.App.Save(state.Job); err != nil {
		return fmt.Errorf("save metadata snapshot: %w", err)
	}
	return nil
}

type ApplyMetadataStep struct{}

func (s *ApplyMetadataStep) Name() string { return models.StepApplyMetadata }

func (s *ApplyMetadataStep) ShouldSkip(state *StepState) (bool, error) {
	if state.Document != nil && state.Document.GetString("duplicate_of") != "" {
		return true, nil
	}
	return false, nil
}

func (s *ApplyMetadataStep) Run(ctx context.Context, state *StepState) error {
	_ = ctx

	metadata := state.Metadata
	if metadata == nil {
		var err error
		metadata, err = loadMetadataJSON(state.Job)
		if err != nil {
			return err
		}
	}
	if metadata == nil {
		return fmt.Errorf("apply_metadata requires metadata_json")
	}

	applyExtractedMetadata(state.Document, metadata, state.Cfg.ProcessingResultLanguage)
	if err := applyDocumentType(state.App, state.Document, metadata, state.Cfg.ProcessingResultLanguage); err != nil {
		return fmt.Errorf("document type: %w", err)
	}
	state.Logger.Info("document type applied",
		"document_type", truncateForLog(state.Document.GetString("document_type"), 40),
	)

	if err := applyCorrespondent(state.App, state.Document, metadata, state.Cfg.ProcessingResultLanguage); err != nil {
		return fmt.Errorf("correspondent: %w", err)
	}
	state.Logger.Info("correspondent applied",
		"correspondent", truncateForLog(state.Document.GetString("correspondent"), 40),
	)

	state.Document.Set("confidence", metadata.Confidence)
	state.Document.Set("people_or_organizations", metadata.PeopleOrOrganizations)
	state.Document.Set("metadata_source", state.AI.Model())

	if metadata.DocumentDate != "" {
		state.Document.Set("document_date", metadata.DocumentDate)
	}

	tagIDs, err := ensureTags(state.App, mergeTagNames(metadata.Tags, metadata.TagsTranslated))
	if err != nil {
		return fmt.Errorf("tags: %w", err)
	}
	state.Document.Set("tags", tagIDs)
	state.Logger.Info("tags applied", "count", len(tagIDs))

	status := models.DocStatusCompleted
	jobStatus := models.JobStatusCompleted
	if metadata.Confidence < 0.5 {
		status = models.DocStatusNeedsReview
		jobStatus = models.JobStatusNeedsReview
	}

	state.Document.Set("processing_status", status)
	if err := state.App.Save(state.Document); err != nil {
		return fmt.Errorf("save document: %w", err)
	}

	state.Job.Set("status", jobStatus)
	return nil
}
