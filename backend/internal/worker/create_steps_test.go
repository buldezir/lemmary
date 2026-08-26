package worker

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func newTestCreateStepsRecord() *core.Record {
	documents := core.NewBaseCollection("documents")
	documents.Fields.Add(&core.TextField{Name: "checksum"})
	return core.NewRecord(documents)
}

func TestCreateStepsForDefaultsToFullPipeline(t *testing.T) {
	t.Parallel()

	if got := createStepsFor(newTestCreateStepsRecord(), nil); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("default steps=%v", got)
	}
	if got := createStepsFor(nil, nil); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("nil record steps=%v", got)
	}
}

func TestSetCreateStepsIsReadBack(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	SetCreateSteps(record, models.ImportPreserveSteps)

	if got := createStepsFor(record, nil); !slices.Equal(got, models.ImportPreserveSteps) {
		t.Fatalf("registered steps=%v", got)
	}
}

func TestSetCreateStepsIgnoresEmpty(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	SetCreateSteps(record, nil)
	SetCreateSteps(record, []string{})

	if got := createStepsFor(record, nil); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("expected full pipeline, got %v", got)
	}
}

// The transient key must not be persisted: DBExport is what the DB write uses.
func TestCreateStepsAreNotPersisted(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	SetCreateSteps(record, models.ImportPreserveSteps)

	if _, ok := record.FieldsData()[createStepsKey]; ok {
		t.Fatalf("expected %q to stay out of the persisted field data", createStepsKey)
	}
}

func TestSetCreateStepsCopiesInput(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	steps := []string{models.StepPreview, models.StepOCR}
	SetCreateSteps(record, steps)
	steps[0] = "mutated"

	if got := createStepsFor(record, nil); got[0] != models.StepPreview {
		t.Fatalf("expected stored steps to be unaffected by caller mutation, got %v", got)
	}
}

func TestCreateStepsForAppliesStepPlansToTheDefault(t *testing.T) {
	t.Parallel()

	dropOCR := func(steps []string) []string {
		out := make([]string, 0, len(steps))
		for _, step := range steps {
			if step != models.StepOCR {
				out = append(out, step)
			}
		}
		return out
	}

	got := createStepsFor(newTestCreateStepsRecord(), []StepPlan{dropOCR})
	if slices.Contains(got, models.StepOCR) {
		t.Fatalf("expected the plan to drop %q, got %v", models.StepOCR, got)
	}
	if len(got) != len(models.FullPipelineSteps)-1 {
		t.Fatalf("expected one step fewer than the default pipeline, got %v", got)
	}
}

// An explicit request is the caller naming the stages it needs; a plan that
// rewrote it would re-run OCR on a reprocess that asked only for metadata.
func TestStepPlansDoNotTouchExplicitlyRequestedSteps(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	SetCreateSteps(record, models.ImportPreserveSteps)

	plan := func([]string) []string { return []string{models.StepOCR} }
	if got := createStepsFor(record, []StepPlan{plan}); !slices.Equal(got, models.ImportPreserveSteps) {
		t.Fatalf("expected the explicit request to win, got %v", got)
	}
}

// models.FullPipelineSteps is shared by every caller in the process: a plan that
// appends to the slice it is handed must not reach the next upload.
func TestStepPlansCannotCorruptTheDefaultPipeline(t *testing.T) {
	t.Parallel()

	before := append([]string(nil), models.FullPipelineSteps...)

	appendJunk := func(steps []string) []string { return append(steps, "junk") }
	createStepsFor(newTestCreateStepsRecord(), []StepPlan{appendJunk})

	if !slices.Equal(models.FullPipelineSteps, before) {
		t.Fatalf("plan mutated the shared default pipeline: %v", models.FullPipelineSteps)
	}
	if got := createStepsFor(newTestCreateStepsRecord(), nil); !slices.Equal(got, before) {
		t.Fatalf("later upload saw a corrupted default: %v", got)
	}
}
