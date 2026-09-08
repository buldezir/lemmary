package worker

import (
	"testing"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
)

func TestFinishedDocStatusHonoursConfidenceAndThePolicy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                string
		alwaysRequireReview bool
		lowConfidence       bool
		want                string
	}{
		{
			name: "a confident extraction completes on its own",
			want: models.DocStatusCompleted,
		},
		{
			name:          "a doubtful one waits whatever the policy is",
			lowConfidence: true,
			want:          models.DocStatusNeedsReview,
		},
		{
			// The whole point of the setting: metadata nobody has read does not
			// count as done just because the model sounded sure.
			name:                "the policy sends a confident one to the Inbox too",
			alwaysRequireReview: true,
			want:                models.DocStatusNeedsReview,
		},
		{
			name:                "the two reasons do not cancel out",
			alwaysRequireReview: true,
			lowConfidence:       true,
			want:                models.DocStatusNeedsReview,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := config.Config{AlwaysRequireReview: tc.alwaysRequireReview}
			if got := finishedDocStatus(cfg, tc.lowConfidence); got != tc.want {
				t.Fatalf("finishedDocStatus = %q, want %q", got, tc.want)
			}
		})
	}
}

// The setting reaches only the apply_metadata step, so a step list without it
// finishes on completed however the policy is set. That is deliberate: the list
// without apply is above all a paperless-ngx import, whose metadata was curated
// in the other system and never touched by a model here -- requiring review of
// it would empty a migrated archive into the Inbox for extraction that never
// ran. This is the line that makes the exemption true, so it is the line worth
// pinning: see finalizeDocumentWithoutApply, and TestImportPreserveStepsEmbed
// in internal/models for the other half.
func TestThePolicyCannotReachAStepListWithoutApplyMetadata(t *testing.T) {
	t.Parallel()
	for _, step := range models.ImportPreserveSteps {
		if step == models.StepApplyMetadata {
			t.Fatalf("ImportPreserveSteps runs apply_metadata, so an ngx import would "+
				"now require review: %v", models.ImportPreserveSteps)
		}
	}
}

// Mirrored as LOW_CONFIDENCE_THRESHOLD in frontend/src/lib/documentStatus.ts,
// which words the "why is this waiting" line on a card. Drift would not change
// a status, but it would make the card explain the wrong reason.
func TestTheConfidenceThresholdIsTheOneTheFrontendMirrors(t *testing.T) {
	t.Parallel()
	if minExtractionConfidence != 0.5 {
		t.Fatalf("minExtractionConfidence = %v; update LOW_CONFIDENCE_THRESHOLD to match",
			minExtractionConfidence)
	}
}
