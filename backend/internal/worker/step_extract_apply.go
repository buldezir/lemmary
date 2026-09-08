package worker

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/logfmt"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/strutil"
)

// minExtractionConfidence is the score below which the pipeline sends a
// document for review on its own, whatever the instance's policy is. Mirrored
// as LOW_CONFIDENCE_THRESHOLD in frontend/src/lib/documentStatus.ts, which
// reads it only to word the "why is this waiting" line on a card.
const minExtractionConfidence = 0.5

// finishedDocStatus is the status a pipeline run leaves on a document that
// neither failed nor turned out to be a duplicate.
//
// With AlwaysRequireReview on, nothing here ever returns completed: a person
// saying so is the only way a document leaves the Inbox. That includes
// reprocessed documents, which is the point -- a reprocess produces fresh
// model output that nobody has read.
func finishedDocStatus(cfg config.Config, lowConfidence bool) string {
	if lowConfidence || cfg.AlwaysRequireReview {
		return models.DocStatusNeedsReview
	}
	return models.DocStatusCompleted
}

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
	if s.Extractor == nil {
		return fmt.Errorf("extract_metadata requires a configured AI extractor")
	}

	ocrText := strings.TrimSpace(state.OCRText)
	if ocrText == "" {
		ocrText = strings.TrimSpace(state.Document.GetString("ocr_text"))
	}
	if ocrText == "" {
		return fmt.Errorf("extract_metadata requires ocr_text")
	}
	state.OCRText = ocrText

	userID := strings.TrimSpace(state.Document.GetString("user"))
	catalog := loadExtractionCatalog(state.App, userID, state.Logger)

	state.Logger.Info("starting AI extraction",
		"provider", s.Extractor.Name(),
		"model", s.Extractor.Model(),
		"ocr_chars", len(ocrText),
		"catalog_correspondent_names", len(catalog.Correspondents),
		"catalog_document_type_names", len(catalog.DocumentTypes),
	)

	aiStart := time.Now()
	aiCtx, cancel := context.WithTimeout(ctx, state.Cfg.OpenAITimeout)
	defer cancel()

	metadata, err := s.Extractor.ExtractMetadata(aiCtx, ocrText, catalog)
	if err != nil {
		state.Logger.Error("AI extraction failed",
			logfmt.Duration("duration", time.Since(aiStart)),
			slog.Any("error", err),
		)
		return fmt.Errorf("ai extraction: %w", err)
	}

	state.Logger.Info("AI extraction complete",
		logfmt.Duration("duration", time.Since(aiStart)),
		"confidence", metadata.Confidence,
		"title", strutil.TruncateRunes(metadata.Title, 80),
		"type", strutil.TruncateRunes(metadata.DocumentType, 40),
		"tags", len(metadata.Tags),
	)

	state.Metadata = metadata
	saveMetadataJSON(state.Job, metadata)
	if err := state.App.Save(state.Job); err != nil {
		return fmt.Errorf("save metadata snapshot: %w", err)
	}
	return nil
}

func loadExtractionCatalog(app core.App, userID string, logger *slog.Logger) ai.ExtractionCatalog {
	if strings.TrimSpace(userID) == "" {
		if logger != nil {
			logger.Warn("extraction catalog skipped: document has no owner")
		}
		return ai.ExtractionCatalog{}
	}

	correspondents, err := listCorrespondentNames(app, userID)
	if err != nil {
		if logger != nil {
			logger.Warn("extraction catalog correspondents unavailable; continuing without them", slog.Any("error", err))
		}
		correspondents = nil
	}
	documentTypes, err := listDocumentTypeNames(app, userID)
	if err != nil {
		if logger != nil {
			logger.Warn("extraction catalog document types unavailable; continuing without them", slog.Any("error", err))
		}
		documentTypes = nil
	}
	return ai.ExtractionCatalog{
		Correspondents: correspondents,
		DocumentTypes:  documentTypes,
	}
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
		"document_type", strutil.TruncateRunes(state.Document.GetString("document_type"), 40),
	)

	if err := applyCorrespondent(state.App, state.Document, metadata, state.Cfg.ProcessingResultLanguage); err != nil {
		return fmt.Errorf("correspondent: %w", err)
	}
	state.Logger.Info("correspondent applied",
		"correspondent", strutil.TruncateRunes(state.Document.GetString("correspondent"), 40),
	)

	state.Document.Set("confidence", metadata.Confidence)
	state.Document.Set("people_or_organizations", metadata.PeopleOrOrganizations)
	if state.AI != nil {
		state.Document.Set("metadata_source", state.AI.Model())
	}

	if metadata.DocumentDate != "" {
		state.Document.Set("document_date", metadata.DocumentDate)
	}

	tagIDs, err := ensureTags(state.App, state.Document.GetString("user"), mergeTagNames(metadata.Tags, metadata.TagsTranslated))
	if err != nil {
		return fmt.Errorf("tags: %w", err)
	}
	state.Document.Set("tags", tagIDs)
	state.Logger.Info("tags applied", "count", len(tagIDs))

	lowConfidence := metadata.Confidence < minExtractionConfidence

	// The job and the document part ways here, and only for the setting: the
	// job says whether processing worked, and a confident extraction that
	// merely awaits a human worked fine. Job status is read as a *processing*
	// outcome -- by the paperless task list (ngxapi.mapTaskStatus) and by
	// Management's counts -- so putting every job in needs_review would empty
	// the word of meaning.
	jobStatus := models.JobStatusCompleted
	if lowConfidence {
		jobStatus = models.JobStatusNeedsReview
	}

	state.Document.Set("processing_status", finishedDocStatus(state.Cfg, lowConfidence))
	if err := state.App.Save(state.Document); err != nil {
		return fmt.Errorf("save document: %w", err)
	}

	state.Job.Set("status", jobStatus)
	return nil
}
