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

// Mirrored as LOW_CONFIDENCE_THRESHOLD in frontend/src/lib/documentStatus.ts,
// which reads it only to word the "why is this waiting" line on a card.
const minExtractionConfidence = 0.5

// With AlwaysRequireReview on it never returns completed, reprocessed documents
// included: a reprocess produces fresh model output that nobody has read.
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
	catalog, err := loadExtractionCatalog(state.App, userID, state.Logger)
	if err != nil {
		return err
	}
	catalog.SuggestNewTags = state.Cfg.AlwaysRequireReview

	state.Logger.Info("starting AI extraction",
		"provider", s.Extractor.Name(),
		"model", s.Extractor.Model(),
		"ocr_chars", len(ocrText),
		"catalog_correspondent_names", len(catalog.Correspondents),
		"catalog_document_type_names", len(catalog.DocumentTypes),
		"catalog_tag_names", len(catalog.Tags),
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

// The names the extraction prompt offers the model.
//
// The correspondent and document-type lists are advisory: they exist so the
// model reuses a name rather than coining a variant, and a failure is logged
// and the document extracted without them.
//
// The tag list is not advisory. It is the complete set of legal answers, and
// apply writes whatever comes back over the document's tags, so an empty list
// reads as "this archive has no tags" and clears what a reprocess was meant to
// leave alone. A tag list that cannot be read fails the step instead.
func loadExtractionCatalog(app core.App, userID string, logger *slog.Logger) (ai.ExtractionCatalog, error) {
	if strings.TrimSpace(userID) == "" {
		// Apply cannot resolve tags without an owner either.
		return ai.ExtractionCatalog{}, fmt.Errorf("extraction catalog: document has no owner")
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
	tags, err := listTagNames(app, userID)
	if err != nil {
		return ai.ExtractionCatalog{}, fmt.Errorf("extraction catalog tags: %w", err)
	}
	// The prompt calls the array the complete set of legal tags, so a vocabulary
	// past the cap makes that a lie: the names beyond it are never offered. The
	// symptom, some tags never assigned, looks like nothing otherwise.
	if logger != nil && len(tags) >= ai.MaxExtractionCatalogNames {
		logger.Warn("tag vocabulary is larger than the extraction catalog holds; tags past the cap are never offered to the model",
			"cap", ai.MaxExtractionCatalogNames,
		)
	}
	return ai.ExtractionCatalog{
		Correspondents: correspondents,
		DocumentTypes:  documentTypes,
		Tags:           tags,
	}, nil
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

	// In review mode the proposals join the closed list: a proposal naming an
	// existing tag is that tag, and an invented name the model put in tags is
	// still a proposal. Off, nobody would accept one, so they are discarded.
	tagNames := metadata.Tags
	if state.Cfg.AlwaysRequireReview {
		tagNames = append(append([]string{}, metadata.Tags...), metadata.SuggestedTags...)
	}
	tagIDs, droppedTags, err := addMatchedTags(state.App, state.Document, tagNames)
	if err != nil {
		return fmt.Errorf("tags: %w", err)
	}
	// The model ignoring its catalog: a document that keeps proposing the same
	// absent name is the archive telling its owner which tag to create.
	state.Logger.Info("tags applied", "count", len(tagIDs), "dropped", droppedTags)

	metadata.SuggestedTags = nil
	if state.Cfg.AlwaysRequireReview {
		metadata.SuggestedTags = pendingTagSuggestions(droppedTags)
	}
	saveMetadataJSON(state.Job, metadata)
	if err := state.App.Save(state.Job); err != nil {
		return fmt.Errorf("save metadata snapshot: %w", err)
	}

	lowConfidence := metadata.Confidence < minExtractionConfidence

	// The job and the document part ways for the setting, not for confidence:
	// job status is read as a processing outcome (ngxapi.mapTaskStatus,
	// Maintenance's counts), and an extraction awaiting a human worked fine.
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
