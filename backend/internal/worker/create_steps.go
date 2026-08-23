package worker

import (
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// createStepsKey carries requested pipeline steps on an unsaved document record.
//
// PocketBase keeps values whose key does not match a collection field in the
// record's in-memory data only — DBExport writes schema fields, so this never
// reaches the database. That makes it a safe transient channel from the caller
// that builds the record to the OnRecordCreate hook that creates the job.
const createStepsKey = "__pipeline_steps"

// SetCreateSteps requests non-default pipeline steps for the job created when
// record is saved. Pass nil or an empty slice to use the full pipeline.
func SetCreateSteps(record *core.Record, steps []string) {
	if record == nil || len(steps) == 0 {
		return
	}
	record.Set(createStepsKey, append([]string(nil), steps...))
}

// createStepsFor returns the steps requested via SetCreateSteps, or the full
// pipeline when none were requested.
func createStepsFor(record *core.Record) []string {
	if record == nil {
		return models.FullPipelineSteps
	}
	steps, ok := record.GetRaw(createStepsKey).([]string)
	if !ok || len(steps) == 0 {
		return models.FullPipelineSteps
	}
	return steps
}
