package worker

import (
	"context"
	"testing"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/ocr"
)

// fakeStep stands in for whatever an edition registers. It records the clients
// it was handed so the test can prove the factory is called with the live ones
// rather than with nil.
type fakeStep struct {
	name string
	ocr  ocr.Provider
	ai   ai.Extractor
}

func (s *fakeStep) Name() string                          { return s.name }
func (s *fakeStep) ShouldSkip(*StepState) (bool, error)   { return false, nil }
func (s *fakeStep) Run(context.Context, *StepState) error { return nil }

func TestBuildRegistryWithoutExtraStepsIsTheCorePipeline(t *testing.T) {
	t.Parallel()

	registry := buildRegistry(stubOCR{}, stubExtractor{}, nil)

	if len(registry) != len(models.FullPipelineSteps) {
		t.Fatalf("registry has %d steps, want %d", len(registry), len(models.FullPipelineSteps))
	}
	for _, name := range models.FullPipelineSteps {
		if _, ok := registry[name]; !ok {
			t.Fatalf("core step %q missing from registry", name)
		}
	}
}

func TestBuildRegistryAddsExtraSteps(t *testing.T) {
	t.Parallel()

	factory := func(o ocr.Provider, a ai.Extractor) Step {
		return &fakeStep{name: "edition_only", ocr: o, ai: a}
	}

	registry := buildRegistry(stubOCR{}, stubExtractor{}, []StepFactory{factory})

	added, ok := registry["edition_only"].(*fakeStep)
	if !ok {
		t.Fatalf("edition step missing from registry")
	}
	if added.ocr == nil || added.ai == nil {
		t.Fatalf("factory was called without the live clients: ocr=%v ai=%v", added.ocr, added.ai)
	}
	if len(registry) != len(models.FullPipelineSteps)+1 {
		t.Fatalf("registry has %d steps, want %d", len(registry), len(models.FullPipelineSteps)+1)
	}
}

// Overriding by name is how an edition changes an existing stage without
// restating the pipeline: the job's steps list still names "ocr".
func TestBuildRegistryExtraStepReplacesBuiltinWithTheSameName(t *testing.T) {
	t.Parallel()

	factory := func(ocr.Provider, ai.Extractor) Step {
		return &fakeStep{name: models.StepOCR}
	}

	registry := buildRegistry(stubOCR{}, stubExtractor{}, []StepFactory{factory})

	if _, ok := registry[models.StepOCR].(*fakeStep); !ok {
		t.Fatalf("expected the edition step to replace the built-in %q, got %T", models.StepOCR, registry[models.StepOCR])
	}
	if len(registry) != len(models.FullPipelineSteps) {
		t.Fatalf("replacing a step changed the registry size to %d", len(registry))
	}
}

// A factory that declines to build (no API key for the service it wraps, say)
// must leave the core pipeline intact rather than nil out a stage.
func TestBuildRegistrySkipsFactoriesReturningNil(t *testing.T) {
	t.Parallel()

	factory := func(ocr.Provider, ai.Extractor) Step { return nil }

	registry := buildRegistry(stubOCR{}, stubExtractor{}, []StepFactory{factory})

	if len(registry) != len(models.FullPipelineSteps) {
		t.Fatalf("registry has %d steps, want %d", len(registry), len(models.FullPipelineSteps))
	}
}
