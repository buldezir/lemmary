package worker

import (
	"sync"

	"paperless-go/backend/internal/models"
)

// createStepsByChecksum lets a caller (e.g. ngx import) request non-default
// pipeline steps for the next document create with that checksum. The create
// hook consumes the entry with LoadAndDelete so concurrent uploads with other
// checksums are unaffected.
var createStepsByChecksum sync.Map

// RegisterCreateStepsForChecksum schedules custom processing steps for the
// next OnRecordCreate of a document whose checksum matches. Callers should
// ClearCreateStepsForChecksum if Save fails before the hook runs.
func RegisterCreateStepsForChecksum(checksum string, steps []string) {
	if checksum == "" || len(steps) == 0 {
		return
	}
	copied := append([]string(nil), steps...)
	createStepsByChecksum.Store(checksum, copied)
}

// ClearCreateStepsForChecksum drops a pending registration (e.g. after a failed Save).
func ClearCreateStepsForChecksum(checksum string) {
	if checksum == "" {
		return
	}
	createStepsByChecksum.Delete(checksum)
}

func takeCreateStepsForChecksum(checksum string) []string {
	if checksum == "" {
		return models.FullPipelineSteps
	}
	if v, ok := createStepsByChecksum.LoadAndDelete(checksum); ok {
		if steps, ok := v.([]string); ok && len(steps) > 0 {
			return steps
		}
	}
	return models.FullPipelineSteps
}
