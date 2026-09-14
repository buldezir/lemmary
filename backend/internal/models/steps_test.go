package models

import "testing"

func TestEmbedRunsAfterApplyMetadata(t *testing.T) {
	t.Parallel()

	for name, steps := range map[string][]string{
		"full":       FullPipelineSteps,
		"extraction": ExtractionPipelineSteps,
	} {
		t.Run(name, func(t *testing.T) {
			apply, embed := -1, -1
			for i, step := range steps {
				switch step {
				case StepApplyMetadata:
					apply = i
				case StepEmbed:
					embed = i
				}
			}
			if embed < 0 {
				t.Fatalf("%v does not run the embed step", steps)
			}
			if apply < 0 || embed < apply {
				t.Fatalf("embed at %d must come after apply_metadata at %d in %v", embed, apply, steps)
			}
			if embed != len(steps)-1 {
				t.Fatalf("embed should be the last step in %v", steps)
			}
		})
	}
}

func TestImportPreserveStepsEmbed(t *testing.T) {
	t.Parallel()

	last := ImportPreserveSteps[len(ImportPreserveSteps)-1]
	if last != StepEmbed {
		t.Fatalf("ImportPreserveSteps ends with %q, want %q", last, StepEmbed)
	}
	for _, step := range ImportPreserveSteps {
		if step == StepApplyMetadata || step == StepExtractMetadata {
			t.Fatalf("ImportPreserveSteps must not run AI metadata steps: %v", ImportPreserveSteps)
		}
	}
}
