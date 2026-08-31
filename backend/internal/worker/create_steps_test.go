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

func TestSkipCreateJobIsReadBack(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	if skipsCreateJob(record) {
		t.Fatal("a plain record must still get a job")
	}
	if skipsCreateJob(nil) {
		t.Fatal("nil record must not report a skip")
	}

	SkipCreateJob(record)
	if !skipsCreateJob(record) {
		t.Fatal("expected the skip to be read back")
	}
}

// The transient key must not be persisted: DBExport is what the DB write uses.
func TestSkipCreateJobIsNotPersisted(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	SkipCreateJob(record)

	if _, ok := record.FieldsData()[skipCreateJobKey]; ok {
		t.Fatalf("expected %q to stay out of the persisted field data", skipCreateJobKey)
	}
}

// Skipping the job is not the same as asking for no steps: an empty step list
// means "use the full pipeline", which is why the two need separate keys.
func TestSkipCreateJobIsIndependentOfSteps(t *testing.T) {
	t.Parallel()

	record := newTestCreateStepsRecord()
	SkipCreateJob(record)

	if got := createStepsFor(record); !slices.Equal(got, models.FullPipelineSteps) {
		t.Fatalf("steps=%v", got)
	}

	stepped := newTestCreateStepsRecord()
	SetCreateSteps(stepped, models.ImportPreserveSteps)
	if skipsCreateJob(stepped) {
		t.Fatal("requesting steps must not suppress the job")
	}
}
