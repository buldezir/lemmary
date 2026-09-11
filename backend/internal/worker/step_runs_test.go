package worker

import (
	"strings"
	"testing"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
)

// The extraction step records which prompt produced the metadata. Once an admin
// could add rules, the configured version stopped being the whole answer: the
// prompt changes while extraction_prompt_version stays "v1".
func TestSetStepRunExecutionDetailsRecordsTheRulesWithThePromptVersion(t *testing.T) {
	t.Parallel()

	bare := models.StepRun{Name: models.StepExtractMetadata}
	setStepRunExecutionDetails(&bare, &StepState{Cfg: config.Config{ExtractionPromptVer: "v1"}})
	if bare.PromptVersion != "v1" {
		t.Fatalf("no rules should record the bare version, got %q", bare.PromptVersion)
	}

	withRules := models.StepRun{Name: models.StepExtractMetadata}
	setStepRunExecutionDetails(&withRules, &StepState{Cfg: config.Config{
		ExtractionPromptVer: "v1",
		ExtractionRules:     "Prefer the issuer over the payer.",
	}})
	if !strings.HasPrefix(withRules.PromptVersion, "v1+rules.") {
		t.Fatalf("expected the rules in the recorded prompt, got %q", withRules.PromptVersion)
	}
}
