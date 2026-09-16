package worker

import (
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// PocketBase keeps a value whose key matches no collection field in memory
// only, so this never reaches the database: a transient channel from the
// caller that builds the record to the OnRecordCreate hook.
const createStepsKey = "__pipeline_steps"

// Pass nil or an empty slice to use the full pipeline.
func SetCreateSteps(record *core.Record, steps []string) {
	if record == nil || len(steps) == 0 {
		return
	}
	record.Set(createStepsKey, append([]string(nil), steps...))
}

// skipCreateJobKey suppresses the job entirely, travelling the same way.
const skipCreateJobKey = "__pipeline_skip_job"

// For restoring a backup, where the document arrives already processed. A job
// there is not merely wasted work: the pipeline's own saves race with and
// overwrite the archived timestamps and status the restore writes back. An
// empty step list cannot express this; SetCreateSteps reads it as "full".
func SkipCreateJob(record *core.Record) {
	if record == nil {
		return
	}
	record.Set(skipCreateJobKey, true)
}

func skipsCreateJob(record *core.Record) bool {
	if record == nil {
		return false
	}
	skip, _ := record.GetRaw(skipCreateJobKey).(bool)
	return skip
}

// An explicit request wins outright: the split upload and reprocess paths name
// the stages they need, and the full pipeline would silently re-run OCR on a
// document that only asked to have its metadata reapplied.
func createStepsFor(record *core.Record) []string {
	if record != nil {
		if steps, ok := record.GetRaw(createStepsKey).([]string); ok && len(steps) > 0 {
			return steps
		}
	}
	// A copy: models.FullPipelineSteps is shared by every caller, and one that
	// appended to it would corrupt the default pipeline for later uploads.
	return append([]string(nil), models.FullPipelineSteps...)
}
