package reprocess

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"paperless-go/backend/internal/models"
)

func newTestDocument(ocrText string) *core.Record {
	documents := core.NewBaseCollection("documents")
	documents.Fields.Add(&core.TextField{Name: "ocr_text"})
	record := core.NewRecord(documents)
	record.Set("ocr_text", ocrText)
	return record
}

func TestStepsForAutoSkipsOCRWhenTextSurvived(t *testing.T) {
	t.Parallel()

	steps, _ := StepsFor(newTestDocument("extracted text"), ModeAuto)
	if !slices.Equal(steps, models.ExtractionPipelineSteps) {
		t.Fatalf("steps=%v, want %v", steps, models.ExtractionPipelineSteps)
	}
}

func TestStepsForAutoRunsFullPipelineWithoutText(t *testing.T) {
	t.Parallel()

	// Whitespace-only OCR text is no text at all: the document still needs OCR.
	for _, ocrText := range []string{"", "   \n\t "} {
		steps, _ := StepsFor(newTestDocument(ocrText), ModeAuto)
		if !slices.Equal(steps, models.FullPipelineSteps) {
			t.Fatalf("ocr_text=%q steps=%v, want %v", ocrText, steps, models.FullPipelineSteps)
		}
	}
}

func TestStepsForNilDocumentRunsFullPipeline(t *testing.T) {
	t.Parallel()

	steps, _ := StepsFor(nil, ModeAuto)
	if !slices.Equal(steps, models.FullPipelineSteps) {
		t.Fatalf("steps=%v, want %v", steps, models.FullPipelineSteps)
	}
}

func TestStepsForExplicitModesIgnoreOCRText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		mode Mode
		want []string
	}{
		{mode: ModeFull, want: models.FullPipelineSteps},
		{mode: ModeExtraction, want: models.ExtractionPipelineSteps},
	}
	for _, tc := range cases {
		for _, ocrText := range []string{"", "extracted text"} {
			steps, _ := StepsFor(newTestDocument(ocrText), tc.mode)
			if !slices.Equal(steps, tc.want) {
				t.Fatalf("mode=%s ocr_text=%q steps=%v, want %v", tc.mode, ocrText, steps, tc.want)
			}
		}
	}
}

// Forcing apply_metadata would overwrite metadata the user corrected by hand.
func TestStepsForNeverForcesApplyMetadata(t *testing.T) {
	t.Parallel()

	for _, mode := range []Mode{ModeAuto, ModeFull, ModeExtraction} {
		for _, ocrText := range []string{"", "extracted text"} {
			steps, forceSteps := StepsFor(newTestDocument(ocrText), mode)
			if !slices.Contains(steps, models.StepApplyMetadata) {
				t.Fatalf("mode=%s: expected apply_metadata among steps, got %v", mode, steps)
			}
			if slices.Contains(forceSteps, models.StepApplyMetadata) {
				t.Fatalf("mode=%s: apply_metadata must not be forced, got %v", mode, forceSteps)
			}
			// Everything else that runs is forced, or a skip check would let the
			// already-completed step through untouched.
			for _, step := range steps {
				if step == models.StepApplyMetadata {
					continue
				}
				if !slices.Contains(forceSteps, step) {
					t.Fatalf("mode=%s: step %q is not forced, got %v", mode, step, forceSteps)
				}
			}
		}
	}
}

// StepsFor must not hand back the package-level step slices, or a caller
// mutating its result would corrupt every later job.
func TestStepsForCopiesPipelineConstants(t *testing.T) {
	t.Parallel()

	steps, _ := StepsFor(newTestDocument(""), ModeFull)
	steps[0] = "mutated"

	if models.FullPipelineSteps[0] != models.StepPreview {
		t.Fatalf("FullPipelineSteps was mutated: %v", models.FullPipelineSteps)
	}
}

func TestParseMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		raw  string
		want Mode
	}{
		{raw: "", want: ModeAuto},
		{raw: "  ", want: ModeAuto},
		{raw: "auto", want: ModeAuto},
		{raw: "full", want: ModeFull},
		{raw: " extraction ", want: ModeExtraction},
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.raw)
		if err != nil {
			t.Fatalf("ParseMode(%q) returned %v", tc.raw, err)
		}
		if got != tc.want {
			t.Fatalf("ParseMode(%q)=%q, want %q", tc.raw, got, tc.want)
		}
	}

	for _, raw := range []string{"AUTO", "everything", "ocr"} {
		if _, err := ParseMode(raw); err == nil {
			t.Fatalf("ParseMode(%q) accepted an unknown mode", raw)
		}
	}
}

func TestClampLimit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		limit int
		want  int
	}{
		{limit: 0, want: DefaultLimit},
		{limit: -5, want: DefaultLimit},
		{limit: 1, want: 1},
		{limit: 50, want: 50},
		{limit: MaxLimit, want: MaxLimit},
		{limit: MaxLimit + 1, want: MaxLimit},
		{limit: 1_000_000, want: MaxLimit},
	}
	for _, tc := range cases {
		if got := clampLimit(tc.limit); got != tc.want {
			t.Fatalf("clampLimit(%d)=%d, want %d", tc.limit, got, tc.want)
		}
	}
}

func TestRunBatchRequiresOwner(t *testing.T) {
	t.Parallel()

	if _, err := RunBatch(nil, Request{OwnerUserID: "  "}); err == nil {
		t.Fatal("expected a missing owner to be rejected before any query runs")
	}
}

func TestRunBatchRejectsUnknownMode(t *testing.T) {
	t.Parallel()

	if _, err := RunBatch(nil, Request{OwnerUserID: "user1", Mode: Mode("sideways")}); err == nil {
		t.Fatal("expected an unknown mode to be rejected before any query runs")
	}
}
