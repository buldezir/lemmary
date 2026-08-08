package worker

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/models"
	"paperless-go/backend/internal/textextract"
)

type failingOCRProvider struct{}

func (failingOCRProvider) Name() string { return "failing" }

func (failingOCRProvider) ExtractText(context.Context, string, string) (string, error) {
	return "", errors.New("provider should not be called")
}

func TestNextRunnableIndexSkipsCompleted(t *testing.T) {
	runs := []models.StepRun{
		{Name: models.StepPreview, Status: models.StepStatusCompleted},
		{Name: models.StepOCR, Status: models.StepStatusFailed},
		{Name: models.StepExtractMetadata, Status: models.StepStatusPending},
	}

	if got := nextRunnableIndex(runs); got != 1 {
		t.Fatalf("expected index 1, got %d", got)
	}
}

func TestNextRunnableIndexAllDone(t *testing.T) {
	runs := []models.StepRun{
		{Name: models.StepPreview, Status: models.StepStatusSkipped},
		{Name: models.StepOCR, Status: models.StepStatusCompleted},
	}

	if got := nextRunnableIndex(runs); got != -1 {
		t.Fatalf("expected -1, got %d", got)
	}
}

func TestSyncStepRunsPreservesCompleted(t *testing.T) {
	existing := []models.StepRun{
		{Name: models.StepPreview, Status: models.StepStatusCompleted, Attempts: 1},
		{Name: models.StepOCR, Status: models.StepStatusFailed, Attempts: 2, Error: "timeout"},
	}
	steps := []string{models.StepPreview, models.StepOCR, models.StepExtractMetadata}

	got := syncStepRuns(steps, existing)
	if len(got) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(got))
	}
	if got[0].Status != models.StepStatusCompleted {
		t.Fatalf("expected preview completed, got %q", got[0].Status)
	}
	if got[1].Attempts != 2 || got[1].Error != "timeout" {
		t.Fatalf("expected preserved OCR failure state, got %+v", got[1])
	}
	if got[2].Status != models.StepStatusPending {
		t.Fatalf("expected new step pending, got %q", got[2].Status)
	}
}

func TestInitStepRuns(t *testing.T) {
	steps := models.FullPipelineSteps
	got := initStepRuns(steps)
	if len(got) != len(steps) {
		t.Fatalf("expected %d runs, got %d", len(steps), len(got))
	}
	for i, step := range steps {
		if got[i].Name != step {
			t.Fatalf("run %d name=%q want %q", i, got[i].Name, step)
		}
		if got[i].Status != models.StepStatusPending {
			t.Fatalf("run %d status=%q want pending", i, got[i].Status)
		}
	}
}

func TestStepsInclude(t *testing.T) {
	steps := []string{models.StepOCR, models.StepExtractMetadata}
	if !slices.Contains(steps, models.StepOCR) {
		t.Fatal("expected ocr to be included")
	}
	if slices.Contains(steps, models.StepPreview) {
		t.Fatal("expected preview to be excluded")
	}
}

func TestExtractMetadataShouldSkipWhenSnapshotExists(t *testing.T) {
	jobs := coreTestJobsCollection()
	job := core.NewRecord(jobs)
	saveMetadataJSON(job, &models.ExtractedMetadata{Title: "Invoice"})

	step := &ExtractMetadataStep{}
	state := &StepState{Job: job}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skipped {
		t.Fatal("expected skip when metadata_json exists")
	}
	if state.Metadata == nil || state.Metadata.Title != "Invoice" {
		t.Fatalf("expected metadata loaded into state, got %+v", state.Metadata)
	}
}

func TestExtractMetadataShouldNotSkipWhenSnapshotEmpty(t *testing.T) {
	jobs := coreTestJobsCollection()
	job := core.NewRecord(jobs)
	job.Set("metadata_json", map[string]any{})

	step := &ExtractMetadataStep{}
	state := &StepState{Job: job}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skipped {
		t.Fatal("expected extract_metadata to run when metadata_json is empty")
	}
}

func TestLoadMetadataJSONIgnoresEmptySnapshot(t *testing.T) {
	jobs := coreTestJobsCollection()
	job := core.NewRecord(jobs)
	job.Set("metadata_json", map[string]any{})

	metadata, err := loadMetadataJSON(job)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata != nil {
		t.Fatalf("expected nil metadata for empty snapshot, got %+v", metadata)
	}
}

func TestOCRShouldSkipWhenTextExists(t *testing.T) {
	docs := coreTestDocumentsCollection()
	doc := core.NewRecord(docs)
	doc.Set("ocr_text", "saved text")

	step := &OCRStep{}
	state := &StepState{Document: doc}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skipped {
		t.Fatal("expected skip when ocr_text exists")
	}
	if state.OCRText != "saved text" {
		t.Fatalf("expected OCR text in state, got %q", state.OCRText)
	}
}

func TestOCRShouldNotSkipWhenForced(t *testing.T) {
	docs := coreTestDocumentsCollection()
	doc := core.NewRecord(docs)
	doc.Set("ocr_text", "saved text")

	step := &OCRStep{}
	state := &StepState{
		Document:   doc,
		ForceSteps: map[string]bool{models.StepOCR: true},
	}

	skipped, err := step.ShouldSkip(state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skipped {
		t.Fatal("expected forced OCR not to skip")
	}
}

func TestResolveOCRTextNativeBypassesProvider(t *testing.T) {
	csvPath := filepath.Join("..", "textextract", "testdata", "sample.csv")
	state := &StepState{
		TmpPath:  csvPath,
		MimeType: textextract.MIMECSV,
	}

	text, providerName, err := resolveOCRText(context.Background(), state, failingOCRProvider{})
	if err != nil {
		t.Fatalf("resolveOCRText: %v", err)
	}
	if providerName != "native" {
		t.Fatalf("providerName=%q want native", providerName)
	}
	if !strings.Contains(text, "Paper") {
		t.Fatalf("unexpected text %q", text)
	}
}
