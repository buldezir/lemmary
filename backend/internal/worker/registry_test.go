package worker

import (
	"testing"

	"lemmary/backend/internal/models"
)

// The registry the runner dispatches on is built from a hand-written list of
// steps, while jobs name the steps they want from models.FullPipelineSteps. The
// two are separate declarations of the same pipeline, so a step added to one
// and not the other is a job that fails on an unknown stage.
func TestBuildRegistryIsExactlyTheFullPipeline(t *testing.T) {
	t.Parallel()

	registry := buildRegistry(stubOCR{}, stubExtractor{}, stubEmbedder{})

	if len(registry) != len(models.FullPipelineSteps) {
		t.Fatalf("registry has %d steps, want %d", len(registry), len(models.FullPipelineSteps))
	}
	for _, name := range models.FullPipelineSteps {
		if _, ok := registry[name]; !ok {
			t.Fatalf("step %q is named in FullPipelineSteps but missing from the registry", name)
		}
	}
}
