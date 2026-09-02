package worker

import (
	"context"
	"log/slog"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/ocr"
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
	Embedder ai.Embedder
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

// buildRegistry maps every stage name a job can name to the step that runs it.
//
// Built per reload rather than once at wiring time: the OCR provider and the
// extractor are rebuilt whenever an admin changes the settings, and a registry
// constructed at boot would keep dispatching to the clients that existed then.
func buildRegistry(ocrProvider ocr.Provider, aiExtractor ai.Extractor, embedder ai.Embedder) map[string]Step {
	steps := []Step{
		&PreviewStep{},
		&OCRStep{Provider: ocrProvider},
		&DetectDuplicatesStep{},
		&ExtractMetadataStep{Extractor: aiExtractor},
		&ApplyMetadataStep{},
		&EmbedStep{Embedder: embedder},
	}
	registry := make(map[string]Step, len(steps))
	for _, step := range steps {
		registry[step.Name()] = step
	}
	return registry
}
