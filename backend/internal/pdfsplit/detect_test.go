package pdfsplit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/pdftool/testpdf"
)

type stubSplitter struct {
	pages      []ai.PageText
	suggestion *models.SplitSuggestion
	err        error
}

func (s *stubSplitter) Name() string  { return "stub" }
func (s *stubSplitter) Model() string { return "stub-model" }

func (s *stubSplitter) DetectSplitPoints(_ context.Context, pages []ai.PageText) (*models.SplitSuggestion, error) {
	s.pages = pages
	if s.err != nil {
		return nil, s.err
	}
	return s.suggestion, nil
}

// stubOCR counts atomically: the detection fallback calls the provider from
// several goroutines at once.
type stubOCR struct {
	callCount atomic.Int64
	err       error
}

func (s *stubOCR) Name() string { return "stub-ocr" }

func (s *stubOCR) calls() int { return int(s.callCount.Load()) }

func (s *stubOCR) ExtractText(_ context.Context, filePath, _ string) (string, error) {
	s.callCount.Add(1)
	if s.err != nil {
		return "", s.err
	}
	return "scanned text from " + filepath.Base(filePath), nil
}

// writeSourcePDF puts a pageCount-page PDF in a temp dir and returns its path.
func writeSourcePDF(t *testing.T, pageCount int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), sourceFileName)
	if err := os.WriteFile(path, testpdf.Multipage(pageCount, "Invoice INV-1001", "Acme Plumbing GmbH"), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return path
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func noReport(int, int) {}

func TestRunDetectUsesThePDFTextLayer(t *testing.T) {
	source := writeSourcePDF(t, 5)
	splitter := &stubSplitter{suggestion: &models.SplitSuggestion{
		Parts: []models.SuggestedPart{{From: 1, To: 2, Title: "Invoice"}, {From: 3, To: 5}},
	}}
	ocrStub := &stubOCR{}

	suggestion, err := runDetect(discardLogger(), source, 5, DetectDeps{Splitter: splitter, OCR: ocrStub}, noReport)
	if err != nil {
		t.Fatalf("runDetect() error: %v", err)
	}
	if suggestion.TextSource != "pdf" {
		t.Fatalf("text_source=%q want pdf", suggestion.TextSource)
	}
	if ocrStub.calls() != 0 {
		t.Fatalf("a born-digital PDF must not be sent to OCR, got %d calls", ocrStub.calls())
	}
	if len(splitter.pages) != 5 {
		t.Fatalf("splitter saw %d pages, want 5", len(splitter.pages))
	}
	if !strings.Contains(splitter.pages[2].Text, "Page 3") {
		t.Fatalf("page 3 text=%q", splitter.pages[2].Text)
	}
	want := []models.SuggestedPart{{From: 1, To: 2, Title: "Invoice"}, {From: 3, To: 5}}
	if len(suggestion.Parts) != len(want) {
		t.Fatalf("parts=%+v want %+v", suggestion.Parts, want)
	}
	for i := range want {
		if suggestion.Parts[i] != want[i] {
			t.Fatalf("part %d=%+v want %+v", i, suggestion.Parts[i], want[i])
		}
	}
}

func TestRunDetectNormalizesASloppyProposal(t *testing.T) {
	source := writeSourcePDF(t, 4)
	// Gapped and unsorted: the caller must still get an exact cover, because
	// that is all the split endpoint accepts.
	splitter := &stubSplitter{suggestion: &models.SplitSuggestion{
		Parts: []models.SuggestedPart{{From: 4, To: 9}, {From: 1, To: 1}},
	}}

	suggestion, err := runDetect(discardLogger(), source, 4, DetectDeps{Splitter: splitter}, noReport)
	if err != nil {
		t.Fatalf("runDetect() error: %v", err)
	}
	if err := ValidateParts(toParts(suggestion.Parts), 4); err != nil {
		t.Fatalf("normalized parts %+v are not a cover: %v", suggestion.Parts, err)
	}
}

func TestRunDetectFallsBackToOCRForAScan(t *testing.T) {
	// A PDF with no text layer at all is what a scan looks like to pdftotext.
	blankSource := blankTextPDF(t, 3)

	splitter := &stubSplitter{suggestion: &models.SplitSuggestion{
		Parts: []models.SuggestedPart{{From: 1, To: 3}},
	}}
	ocrStub := &stubOCR{}

	suggestion, err := runDetect(discardLogger(), blankSource, 3, DetectDeps{Splitter: splitter, OCR: ocrStub}, noReport)
	if err != nil {
		t.Fatalf("runDetect() error: %v", err)
	}
	if suggestion.TextSource != "ocr" {
		t.Fatalf("text_source=%q want ocr", suggestion.TextSource)
	}
	if ocrStub.calls() != 3 {
		t.Fatalf("ocr calls=%d want 3 (one per page)", ocrStub.calls())
	}
	for i, page := range splitter.pages {
		if !strings.Contains(page.Text, "scanned text") {
			t.Fatalf("page %d did not get OCR text: %q", i+1, page.Text)
		}
	}
}

func TestRunDetectRequiresOCRForAScan(t *testing.T) {
	blankSource := blankTextPDF(t, 3)
	splitter := &stubSplitter{suggestion: &models.SplitSuggestion{}}

	if _, err := runDetect(discardLogger(), blankSource, 3, DetectDeps{Splitter: splitter}, noReport); !errors.Is(err, ErrDetectNoOCR) {
		t.Fatalf("err=%v want ErrDetectNoOCR", err)
	}
}

func TestRunDetectRefusesALongScan(t *testing.T) {
	pages := maxDetectOCRPages + 1
	blankSource := blankTextPDF(t, pages)
	splitter := &stubSplitter{suggestion: &models.SplitSuggestion{}}
	ocrStub := &stubOCR{}

	_, err := runDetect(discardLogger(), blankSource, pages, DetectDeps{Splitter: splitter, OCR: ocrStub}, noReport)
	if !errors.Is(err, ErrDetectTooManyPages) {
		t.Fatalf("err=%v want ErrDetectTooManyPages", err)
	}
	if ocrStub.calls() != 0 {
		t.Fatalf("the page limit must be checked before any OCR call, got %d", ocrStub.calls())
	}
}

func TestRunDetectReportsProgressPerPage(t *testing.T) {
	source := writeSourcePDF(t, 4)
	splitter := &stubSplitter{suggestion: &models.SplitSuggestion{}}

	var seen []int
	report := func(done, total int) {
		if total != 4 {
			t.Fatalf("progress total=%d want 4", total)
		}
		seen = append(seen, done)
	}
	if _, err := runDetect(discardLogger(), source, 4, DetectDeps{Splitter: splitter}, report); err != nil {
		t.Fatalf("runDetect() error: %v", err)
	}
	if len(seen) == 0 || seen[len(seen)-1] != 4 {
		t.Fatalf("progress %v should end at 4", seen)
	}
}

func TestDetectRejectsAnUnknownUpload(t *testing.T) {
	resetStaging(t)

	if _, err := Detect(nil, "owner-a", "missing", DetectDeps{Splitter: &stubSplitter{}}); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("err=%v want ErrUploadNotFound", err)
	}
}

func TestDetectRequiresASplitter(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	stageDir(t, root, "upload-1", "owner-a", 3, time.Now().UTC().Add(time.Hour))

	if _, err := Detect(nil, "owner-a", "upload-1", DetectDeps{}); !errors.Is(err, ErrDetectUnavailable) {
		t.Fatalf("err=%v want ErrDetectUnavailable", err)
	}
}

// blankTextPDF writes a PDF whose pages carry no text at all, which is what a
// scan looks like to pdftotext.
func blankTextPDF(t *testing.T, pageCount int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), sourceFileName)
	if err := os.WriteFile(path, testpdf.Blank(pageCount), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	return path
}

func toParts(suggested []models.SuggestedPart) []Part {
	parts := make([]Part, 0, len(suggested))
	for _, part := range suggested {
		parts = append(parts, Part{From: part.From, To: part.To})
	}
	return parts
}

func TestRunDetectTreatsAMostlyScannedFileAsAScan(t *testing.T) {
	// One text page in front of four blank ones: an averaged threshold would
	// call this born-digital and hand the model four empty pages.
	source := filepath.Join(t.TempDir(), sourceFileName)
	if err := os.WriteFile(source, testpdf.MixedText(5, 1), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	splitter := &stubSplitter{suggestion: &models.SplitSuggestion{}}
	ocrStub := &stubOCR{}

	suggestion, err := runDetect(discardLogger(), source, 5, DetectDeps{Splitter: splitter, OCR: ocrStub}, noReport)
	if err != nil {
		t.Fatalf("runDetect() error: %v", err)
	}
	if suggestion.TextSource != "ocr" {
		t.Fatalf("text_source=%q want ocr", suggestion.TextSource)
	}
	if ocrStub.calls() != 5 {
		t.Fatalf("ocr calls=%d want 5", ocrStub.calls())
	}
}
