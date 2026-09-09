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

// The setting reaches only apply_metadata, so a step list without it finishes
// on completed however the policy is set. That exemption exists for the
// paperless-ngx import, whose metadata no model here ever touched; this is the
// line that makes it true. See finalizeDocumentWithoutApply.
func TestThePolicyCannotReachAStepListWithoutApplyMetadata(t *testing.T) {
	t.Parallel()
	for _, step := range models.ImportPreserveSteps {
		if step == models.StepApplyMetadata {
			t.Fatalf("ImportPreserveSteps runs apply_metadata, so an ngx import would "+
				"now require review: %v", models.ImportPreserveSteps)
		}
	}
}

// Mirrored as LOW_CONFIDENCE_THRESHOLD in frontend/src/lib/documentStatus.ts.
// Drift makes a card explain the wrong reason, not show the wrong status.
func TestTheConfidenceThresholdIsTheOneTheFrontendMirrors(t *testing.T) {
	t.Parallel()
	if minExtractionConfidence != 0.5 {
		t.Fatalf("minExtractionConfidence = %v; update LOW_CONFIDENCE_THRESHOLD to match",
			minExtractionConfidence)
	}
}
