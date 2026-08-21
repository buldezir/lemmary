package worker

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"paperless-go/backend/internal/models"
)

func newTestCreateStepsRecord() *core.Record {
	documents := core.NewBaseCollection("documents")
	documents.Fields.Add(&core.TextField{Name: "checksum"})
	return core.NewRecord(documents)
}

func TestCreateStepsForDefaultsToFullPipeline(t *testing.T) {
	t.Parallel()

	if got := createStepsFor(newTestCreateStepsRecord()); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("default steps=%v", got)
	}
	if got := createStepsFor(nil); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("nil record steps=%v", got)
	}
}

func TestSetCreateStepsIsReadBack(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	SetCreateSteps(record, models.ImportPreserveSteps)

	if got := createStepsFor(record); !slices.Equal(got, models.ImportPreserveSteps) {
		t.Fatalf("registered steps=%v", got)
	}
}

func TestSetCreateStepsIgnoresEmpty(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	SetCreateSteps(record, nil)
	SetCreateSteps(record, []string{})

	if got := createStepsFor(record); !slices.Equal(got, models.FullPipelineSteps) {
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

	if got := createStepsFor(record); got[0] != models.StepPreview {
		t.Fatalf("expected stored steps to be unaffected by caller mutation, got %v", got)
	}
}
