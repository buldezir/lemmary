package worker

import (
	"testing"

	"lemmary/backend/internal/models"
)

// The registry and models.FullPipelineSteps are separate declarations of the
// same pipeline, so a step added to one and not the other fails a job.
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
