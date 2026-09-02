package worker

import (
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func embedTestDocument(t *testing.T, set func(*core.Record)) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection("documents")
	collection.Fields.Add(
		&core.TextField{Name: "ocr_text", Max: models.MaxOCRTextRunes},
		&core.TextField{Name: "duplicate_of", Max: 15},
		&core.TextField{Name: "user", Max: 15},
	)
	record := core.NewRecord(collection)
	record.Set("ocr_text", "Die Rechnung wurde bezahlt.")
	if set != nil {
		set(record)
	}
	return record
}

// The feature being off has to look like a skipped step, not a failed one: a
// self-hosted install with no embedding model would otherwise show every
// document with a red step in its history.
func TestEmbedStepSkipsWhenNoModelIsBound(t *testing.T) {
	t.Parallel()
	step := &EmbedStep{}
	state := &StepState{Document: embedTestDocument(t, nil), Logger: slog.Default()}

	skip, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatalf("ShouldSkip: %v", err)
	}
	if !skip {
		t.Fatal("the embed step must skip when no embedder is configured")
	}
}

func TestEmbedStepSkipsDuplicatesAndEmptyText(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*core.Record){
		"a duplicate is never shown as a result": func(r *core.Record) { r.Set("duplicate_of", "otherdocument1") },
		"empty text has nothing to embed":        func(r *core.Record) { r.Set("ocr_text", "   ") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			step := &EmbedStep{Embedder: stubEmbedder{}}
			state := &StepState{Document: embedTestDocument(t, mutate), Logger: slog.Default()}

			skip, err := step.ShouldSkip(state)
			if err != nil {
				t.Fatalf("ShouldSkip: %v", err)
			}
			if !skip {
				t.Fatal("expected the step to be skipped")
			}
		})
	}
}

func TestEmbedStepRunsForAnOrdinaryDocument(t *testing.T) {
	t.Parallel()
	step := &EmbedStep{Embedder: stubEmbedder{}}
	state := &StepState{Document: embedTestDocument(t, nil), Logger: slog.Default()}

	skip, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatalf("ShouldSkip: %v", err)
	}
	if skip {
		t.Fatal("a document with text and an embedder must not be skipped")
	}
}

func TestEmbedStepIsNamedForThePipeline(t *testing.T) {
	t.Parallel()
	if got := (&EmbedStep{}).Name(); got != models.StepEmbed {
		t.Fatalf("Name() = %q, want %q", got, models.StepEmbed)
	}
}

// A soft failure is recorded as failed but walked past, so the loop must not
// pick it up again -- it would retry the same step forever inside one job.
func TestNextRunnableIndexSkipsSoftFailures(t *testing.T) {
	t.Parallel()
	runs := []models.StepRun{
		{Name: models.StepApplyMetadata, Status: models.StepStatusCompleted},
		{Name: models.StepEmbed, Status: models.StepStatusFailed, Soft: true},
	}

	if got := nextRunnableIndex(runs); got != -1 {
		t.Fatalf("nextRunnableIndex() = %d, want -1", got)
	}
}

// A hard failure still stops the pipeline where it stands.
func TestNextRunnableIndexStopsAtHardFailures(t *testing.T) {
	t.Parallel()
	runs := []models.StepRun{
		{Name: models.StepOCR, Status: models.StepStatusFailed},
		{Name: models.StepEmbed, Status: models.StepStatusPending},
	}

	if got := nextRunnableIndex(runs); got != 0 {
		t.Fatalf("nextRunnableIndex() = %d, want 0", got)
	}
}

func TestMarkStepSoftFailedRecordsBothTheFailureAndThePardon(t *testing.T) {
	t.Parallel()
	run := models.StepRun{Name: models.StepEmbed, Status: models.StepStatusRunning}

	markStepSoftFailed(&run, fmt.Errorf("%w: provider is down", ErrStepSoft))

	if run.Status != models.StepStatusFailed {
		t.Fatalf("status = %q, want failed: a soft failure is still a failure", run.Status)
	}
	if !run.Soft {
		t.Fatal("Soft was not set")
	}
	if run.Error == "" || run.FinishedAt == "" {
		t.Fatalf("run = %+v", run)
	}

	// markStepFailed must clear it, so a hard failure on a later attempt is not
	// mistaken for a pardoned one.
	markStepFailed(&run, errors.New("boom"))
	if run.Soft {
		t.Fatal("a hard failure left Soft set")
	}
}

func TestSetStepRunExecutionDetailsNamesTheEmbeddingModel(t *testing.T) {
	t.Parallel()
	run := models.StepRun{Name: models.StepEmbed}

	setStepRunExecutionDetails(&run, &StepState{Embedder: stubEmbedder{}})

	if run.Provider != "stub-embed" || run.Model != "stub-embed-model" {
		t.Fatalf("run = %+v", run)
	}
}

func TestBackfillBatchFromEnv(t *testing.T) {
	cases := map[string]int{
		"":     defaultBackfillBatch,
		"  ":   defaultBackfillBatch,
		"0":    0,
		"5":    5,
		"2O":   defaultBackfillBatch, // a letter O, the classic typo
		"-1":   defaultBackfillBatch,
		"1000": 1000,
	}
	for raw, want := range cases {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			t.Setenv(EnvEmbeddingBackfillBatch, raw)
			if got := BackfillBatchFromEnv(slog.Default()); got != want {
				t.Fatalf("BackfillBatchFromEnv() = %d, want %d", got, want)
			}
		})
	}
}
