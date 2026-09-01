package models

import "testing"

// The header chunk embeds the document's title, type, correspondent, tags and
// summary, and apply_metadata is what writes them. Embedding before that would
// index the metadata the document arrived with rather than the metadata it
// ended up with.
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

// A preserving import writes no AI metadata, but the document still has text
// and a title from the source system, so it is worth embedding.
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
