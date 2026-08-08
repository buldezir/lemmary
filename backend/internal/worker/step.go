package worker

import (
	"context"
	"log/slog"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/ai"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/models"
	"paperless-go/backend/internal/ocr"
)

type Step interface {
	Name() string
	ShouldSkip(state *StepState) (bool, error)
	Run(ctx context.Context, state *StepState) error
}

type StepState struct {
	App      core.App
	Cfg      config.Config
	Job      *core.Record
	Document *core.Record
	OCR      ocr.Provider
	AI       ai.Extractor
	Logger   *slog.Logger

	TmpPath  string
	MimeType string
	Cleanup  func()
	OCRText  string
	Metadata *models.ExtractedMetadata

	ForceSteps map[string]bool
}

func (s *StepState) forced(stepName string) bool {
	return s.ForceSteps != nil && s.ForceSteps[stepName]
}

func buildRegistry(ocrProvider ocr.Provider, aiExtractor ai.Extractor) map[string]Step {
	steps := []Step{
		&PreviewStep{},
		&OCRStep{Provider: ocrProvider},
		&DetectDuplicatesStep{},
		&ExtractMetadataStep{Extractor: aiExtractor},
		&ApplyMetadataStep{},
	}
	registry := make(map[string]Step, len(steps))
	for _, step := range steps {
		registry[step.Name()] = step
	}
	return registry
}
