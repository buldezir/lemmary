package worker

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/pdftool"
	"lemmary/backend/internal/pdftool/testpdf"
	"lemmary/backend/internal/textextract"
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

func TestSyncStepRunsFromEmpty(t *testing.T) {
	steps := models.FullPipelineSteps
	got := syncStepRuns(steps, nil)
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

// pageLimitedOCR answers with each page's text layer. It refuses a part
// longer than limit pages, and, with a budget, a part that would take the
// pages read since the last wait over it, like a per-minute token limit.
type pageLimitedOCR struct {
	limit, budget, used int
	err                 error
	sent                []int
}

func (p *pageLimitedOCR) Name() string { return "limited" }

func (p *pageLimitedOCR) ExtractText(ctx context.Context, path, _ string) (string, error) {
	n, err := pdftool.PageCount(ctx, path)
	if err != nil {
		return "", err
	}
	p.sent = append(p.sent, n)
	if n > p.limit || (p.budget > 0 && p.used+n > p.budget) {
		return "", p.err
	}
	p.used += n
	texts, err := pdftool.AllPagesText(ctx, path, n)
	return strings.Join(texts, " | "), err
}

var errRateLimited = errors.New(`429 Too Many Requests {"message":"request rejected: your request would exceed the configured rate limit"}`)

func pageModeState(t *testing.T, pages int, mode string) *StepState {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(path, testpdf.Multipage(pages), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return &StepState{
		Document: core.NewRecord(coreTestDocumentsCollection()),
		TmpPath:  path,
		MimeType: "application/pdf",
		Logger:   slog.New(slog.DiscardHandler),
		Cfg:      config.Config{OCRPageMode: mode, OCRTimeout: 10 * time.Second},
	}
}

var errContextLength = errors.New("This model's maximum context length is 128000 tokens")

func TestResolveOCRTextPageModes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      string
		limit     int
		budget    int
		err       error
		wantSent  []int
		wantWaits int
		wantText  string
		wantErr   bool
		wantErrIn string
	}{
		{name: "auto sends a document that fits whole", mode: config.OCRPageModeAuto, limit: 5,
			wantSent: []int{5}, wantText: "Page 1 | Page 2 | Page 3 | Page 4 | Page 5"},
		{name: "auto halves only what is refused", mode: config.OCRPageModeAuto, limit: 2, err: errContextLength,
			wantSent: []int{5, 3, 2, 1, 2}, wantText: "Page 1 | Page 2\n\nPage 3\n\nPage 4 | Page 5"},
		{name: "auto retries a rate-limited part after waiting before halving it", mode: config.OCRPageModeAuto, limit: 2, err: errRateLimited,
			wantSent: []int{5, 3, 3, 2, 1, 2}, wantWaits: 1, wantText: "Page 1 | Page 2\n\nPage 3\n\nPage 4 | Page 5"},
		{name: "auto waits out a spent per-minute budget", mode: config.OCRPageModeAuto, limit: 5, budget: 4, err: errRateLimited,
			wantSent: []int{5, 3, 2, 2}, wantWaits: 1, wantText: "Page 1 | Page 2 | Page 3\n\nPage 4 | Page 5"},
		{name: "auto does not split on other errors", mode: config.OCRPageModeAuto, limit: 2, err: errors.New("invalid api key"),
			wantSent: []int{5}, wantErr: true},
		{name: "auto halves a part too large for the per-minute limit without waiting", mode: config.OCRPageModeAuto, limit: 2,
			err:      errors.New("429 Too Many Requests: Request too large for gpt-4o on tokens per min (TPM): Limit 30000, Requested 50000."),
			wantSent: []int{5, 3, 2, 1, 2}, wantText: "Page 1 | Page 2\n\nPage 3\n\nPage 4 | Page 5"},
		{name: "auto fails when a single page is refused", mode: config.OCRPageModeAuto, limit: 0, err: errContextLength,
			wantSent: []int{5, 3, 2, 1}, wantErr: true, wantErrIn: "page 1: "},
		{name: "auto does not split on a spent quota", mode: config.OCRPageModeAuto, limit: 2,
			err:      errors.New(`429 Too Many Requests {"message":"You exceeded your current quota"}`),
			wantSent: []int{5}, wantErr: true},
		{name: "off never splits", mode: config.OCRPageModeOff, limit: 2, err: errRateLimited,
			wantSent: []int{5}, wantErr: true},
		{name: "always sends one page per request", mode: config.OCRPageModeAlways, limit: 1,
			wantSent: []int{1, 1, 1, 1, 1}, wantText: "Page 1\n\nPage 2\n\nPage 3\n\nPage 4\n\nPage 5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := &pageLimitedOCR{limit: tc.limit, budget: tc.budget, err: tc.err}
			waits := 0
			stubRateLimitSleep(t, func() { waits++; provider.used = 0 })
			text, _, err := resolveOCRText(context.Background(), pageModeState(t, 5, tc.mode), provider)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErrIn != "" && !strings.Contains(err.Error(), tc.wantErrIn) {
				t.Errorf("err = %v, want it to name %q", err, tc.wantErrIn)
			}
			if !slices.Equal(provider.sent, tc.wantSent) {
				t.Errorf("pages per request = %v, want %v", provider.sent, tc.wantSent)
			}
			if text != tc.wantText {
				t.Errorf("text = %q, want %q", text, tc.wantText)
			}
			if waits != tc.wantWaits {
				t.Errorf("waited %d times, want %d", waits, tc.wantWaits)
			}
		})
	}
}

func stubRateLimitSleep(t *testing.T, onWait func()) {
	t.Helper()
	saved := ocrRateLimitSleep
	ocrRateLimitSleep = func(context.Context, time.Duration) { onWait() }
	t.Cleanup(func() { ocrRateLimitSleep = saved })
}

type fixedOCR struct {
	text  string
	err   error
	calls int
}

func (f *fixedOCR) Name() string { return "fixed" }

func (f *fixedOCR) ExtractText(context.Context, string, string) (string, error) {
	f.calls++
	return f.text, f.err
}

// Without a page count nothing can be split: the document goes whole, and a
// refusal of it is reported as the provider worded it.
func TestResolveOCRTextWithoutAPageCount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(path, []byte("not a pdf"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	state := pageModeState(t, 1, config.OCRPageModeAlways)
	state.TmpPath = path

	provider := &fixedOCR{text: "whole"}
	if text, _, err := resolveOCRText(context.Background(), state, provider); err != nil || text != "whole" {
		t.Fatalf("always: text = %q, err = %v; want the whole document read", text, err)
	}

	state.Cfg.OCRPageMode = config.OCRPageModeAuto
	provider = &fixedOCR{err: errRateLimited}
	if _, _, err := resolveOCRText(context.Background(), state, provider); !errors.Is(err, errRateLimited) || provider.calls != 1 {
		t.Fatalf("auto: err = %v after %d calls; want the provider's refusal after one", err, provider.calls)
	}
}

func TestCheckOCRTextFits(t *testing.T) {
	if err := checkOCRTextFits("native", strings.Repeat("a", models.MaxOCRTextRunes)); err != nil {
		t.Fatalf("exactly the ceiling should pass: %v", err)
	}

	err := checkOCRTextFits("google_vision", strings.Repeat("a", models.MaxOCRTextRunes+1))
	if err == nil {
		t.Fatal("expected a refusal one character over the ceiling")
	}
	// Naming the provider and the count is why this exists rather than letting
	// app.Save raise validation_max_text_constraint.
	if !strings.Contains(err.Error(), "google_vision") {
		t.Fatalf("error should name the provider: %v", err)
	}
}

// PocketBase measures a text field's Max as len([]rune(value)), so a multi-byte
// document that fits the column must not be refused for its byte count.
func TestCheckOCRTextFitsCountsRunesNotBytes(t *testing.T) {
	// Three bytes per rune: over the ceiling in bytes, exactly at it in runes.
	text := strings.Repeat("あ", models.MaxOCRTextRunes)
	if len(text) <= models.MaxOCRTextRunes {
		t.Fatalf("fixture is not multi-byte: %d bytes", len(text))
	}
	if err := checkOCRTextFits("native", text); err != nil {
		t.Fatalf("runes fit the column, bytes are not the unit: %v", err)
	}
}

// failJob writes job.error only when no step carries the failure; otherwise the
// UI says "Processing failed" where it could have named the step.
func TestHasRecordedStepFailure(t *testing.T) {
	t.Parallel()

	job := core.NewRecord(coreTestJobsCollection())
	if hasRecordedStepFailure(job) {
		t.Fatal("a job with no step_runs records no step failure")
	}

	saveStepRuns(job, []models.StepRun{{Name: models.StepOCR, Status: models.StepStatusCompleted}})
	if hasRecordedStepFailure(job) {
		t.Fatal("a completed step is not a failure")
	}

	// Soft failures do not count: whatever is failing the job now is something
	// else and needs its own message.
	saveStepRuns(job, []models.StepRun{
		{Name: models.StepEmbed, Status: models.StepStatusFailed, Soft: true, Error: "embeddings: 503"},
	})
	if hasRecordedStepFailure(job) {
		t.Fatal("a soft failure must not suppress the job-level error")
	}

	saveStepRuns(job, []models.StepRun{
		{Name: models.StepOCR, Status: models.StepStatusFailed, Error: "mistral: 429"},
	})
	if !hasRecordedStepFailure(job) {
		t.Fatal("a hard step failure already carries the message")
	}
}
