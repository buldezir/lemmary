package worker

import (
	"slices"
	"testing"

	"paperless-go/backend/internal/models"
)

func TestTakeCreateStepsForChecksum(t *testing.T) {
	t.Parallel()

	checksum := "abc123-create-steps-test"
	ClearCreateStepsForChecksum(checksum)

	if got := takeCreateStepsForChecksum(checksum); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("default steps=%v", got)
	}

	RegisterCreateStepsForChecksum(checksum, models.ImportPreserveSteps)
	got := takeCreateStepsForChecksum(checksum)
	if !slices.Equal(got, models.ImportPreserveSteps) {
		t.Fatalf("registered steps=%v", got)
	}
	if got := takeCreateStepsForChecksum(checksum); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("second take should default, got %v", got)
	}

	RegisterCreateStepsForChecksum(checksum, models.ImportPreserveSteps)
	ClearCreateStepsForChecksum(checksum)
	if got := takeCreateStepsForChecksum(checksum); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("cleared steps=%v", got)
	}
}
