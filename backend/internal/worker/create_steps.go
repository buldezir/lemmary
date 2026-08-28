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

// skipCreateJobKey suppresses the job entirely, travelling the same way.
const skipCreateJobKey = "__pipeline_skip_job"

// SkipCreateJob asks for no processing job at all when record is saved.
//
// It exists for restoring a backup, where the document arrives already
// processed: its OCR text, metadata and thumbnail come out of the archive, and
// there is nothing left for the pipeline to derive. Handing it a job anyway is
// not merely wasted work — the pipeline saves the record as it runs, and those
// saves race with (and overwrite) the archived created/updated timestamps and
// processing_status the restore writes back. An empty step list cannot express
// this: SetCreateSteps reads that as "use the full pipeline".
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

// createStepsFor returns the steps requested via SetCreateSteps, or the full
// pipeline as the edition's plans left it when none were requested.
//
// An explicit request wins outright: the split upload and the reprocess paths
// name the stages they need, and a plan that rewrote those lists would silently
// re-run OCR on a document that was only asked to have its metadata reapplied.
func createStepsFor(record *core.Record, plans []StepPlan) []string {
	if record != nil {
		if steps, ok := record.GetRaw(createStepsKey).([]string); ok && len(steps) > 0 {
			return steps
		}
	}
	return applyStepPlans(models.FullPipelineSteps, plans)
}

// applyStepPlans runs plans in order over a copy of steps.
//
// The copy is not defensive tidiness: models.FullPipelineSteps is a
// package-level slice shared by every caller in the process, and a plan that
// appended to or reordered the slice it was handed would corrupt the default
// pipeline for every later upload.
func applyStepPlans(steps []string, plans []StepPlan) []string {
	out := append([]string(nil), steps...)
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		out = plan(out)
	}
	return out
}
