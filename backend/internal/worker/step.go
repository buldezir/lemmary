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

// StepFactory builds a pipeline step from the clients the runner was given.
//
// It is a factory rather than a Step because the OCR provider and the extractor
// are rebuilt on every settings reload: a step constructed once at wiring time
// would capture whichever clients happened to exist at boot and keep using them
// after an admin changed the API key.
type StepFactory func(ocrProvider ocr.Provider, aiExtractor ai.Extractor) Step

// StepPlan rewrites the default step list for a job created from a newly
// uploaded document. See Options.StepPlans.
type StepPlan func(steps []string) []string

func buildRegistry(ocrProvider ocr.Provider, aiExtractor ai.Extractor, extra []StepFactory) map[string]Step {
	steps := []Step{
		&PreviewStep{},
		&OCRStep{Provider: ocrProvider},
		&DetectDuplicatesStep{},
		&ExtractMetadataStep{Extractor: aiExtractor},
		&ApplyMetadataStep{},
	}
	registry := make(map[string]Step, len(steps)+len(extra))
	for _, step := range steps {
		registry[step.Name()] = step
	}
	// Registered last so a factory returning a step whose Name() matches a
	// built-in replaces it. Overriding by name rather than by position is what
	// lets an edition change one stage without restating the pipeline, and the
	// job's own steps list keeps naming the same stage either way.
	for _, factory := range extra {
		step := factory(ocrProvider, aiExtractor)
		if step == nil {
			continue
		}
		registry[step.Name()] = step
	}
	return registry
}
