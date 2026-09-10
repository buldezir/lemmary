package worker

import (
	"testing"

	"lemmary/backend/internal/models"
)

// The retry case is the one worth pinning: handleStepFailure re-pends the job
// and returns no error, so a run that will be attempted again is
// indistinguishable from a successful one unless the status is what decides.
func TestJobOutcome(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		models.JobStatusCompleted:   "completed",
		models.JobStatusFailed:      "failed",
		models.JobStatusNeedsReview: "needs_review",
		models.JobStatusPending:     "retry",
		models.JobStatusRunning:     "running",
		"":                          "unknown",
	}
	for status, want := range cases {
		if got := jobOutcome(status); got != want {
			t.Errorf("jobOutcome(%q) = %q, want %q", status, got, want)
		}
	}
}
