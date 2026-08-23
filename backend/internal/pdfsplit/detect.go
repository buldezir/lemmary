package pdfsplit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/ai"
	"paperless-go/backend/internal/importjob"
	"paperless-go/backend/internal/models"
	"paperless-go/backend/internal/ocr"
	"paperless-go/backend/internal/pdftool"
)

// maxDetectOCRPages bounds the OCR fallback for a scanned file. Each page is a
// separate provider call, so a long scan would take longer than any user is
// willing to wait on a suggestion they can also make by hand.
const maxDetectOCRPages = 40

// minPageTextChars is how much text a page must carry to count as having a text
// layer. A scanned page yields nothing at all, so the bar only has to clear
// stray artifacts.
const minPageTextChars = 16

var (
	// ErrDetectUnavailable is returned when no extraction model is configured.
	ErrDetectUnavailable = errors.New("automatic detection needs an extraction model")
	// ErrDetectNoOCR is returned when a scan needs OCR but none is configured.
	ErrDetectNoOCR = errors.New("automatic detection needs an OCR provider for a scanned PDF")
	// ErrDetectTooManyPages is returned when a scan is too long to OCR page by page.
	ErrDetectTooManyPages = fmt.Errorf("automatic detection reads at most %d pages of a scanned PDF", maxDetectOCRPages)
	// ErrDetectInProgress is returned when another detection is already running.
	ErrDetectInProgress = importjob.ErrBusy
)

// Suggestion is the proposed set of parts for a staged upload.
type Suggestion struct {
	Parts []models.SuggestedPart `json:"parts"`
	// TextSource is "pdf" when the page text came from the PDF itself and "ocr"
	// when the pages had to be read by the OCR provider.
	TextSource string `json:"text_source"`
}

// DetectJob is an in-memory detection run snapshot.
type DetectJob = importjob.Job[Suggestion]

// DetectDeps are the providers a detection run uses. OCR may be nil when the
// PDF carries its own text layer.
type DetectDeps struct {
	Splitter   ai.Splitter
	OCR        ocr.Provider
	OCRTimeout time.Duration
	LLMTimeout time.Duration
}

// detectRegistry is separate from the split registry on purpose: proposing
// boundaries and creating documents are independent runs, and sharing the
// per-owner busy flag would let one block the other.
var detectRegistry = importjob.NewRegistry[Suggestion](importjob.DefaultRetention)

// Detect proposes document boundaries for a staged upload in the background and
// returns the job id. The upload is not consumed: detection can be repeated and
// the user still confirms the split afterwards.
func Detect(app core.App, ownerUserID, uploadID string, deps DetectDeps) (string, error) {
	view, sourcePath, ok := Lookup(strings.TrimSpace(uploadID), ownerUserID)
	if !ok {
		return "", ErrUploadNotFound
	}
	if deps.Splitter == nil {
		return "", ErrDetectUnavailable
	}

	logger := app.Logger().With("component", "pdf_split")
	return detectRegistry.Start(ownerUserID, func(report func(done, total int)) (Suggestion, error) {
		return runDetect(logger, sourcePath, view.PageCount, deps, report)
	})
}

// GetDetectJob returns a copy of the in-memory detection job, or false if unknown.
func GetDetectJob(id string) (DetectJob, bool) {
	return detectRegistry.Get(id)
}

func runDetect(logger *slog.Logger, sourcePath string, pageCount int, deps DetectDeps, report func(done, total int)) (Suggestion, error) {
	report(0, pageCount)

	pages, source, err := readPageText(logger, sourcePath, pageCount, deps, report)
	if err != nil {
		return Suggestion{}, err
	}

	ctx := context.Background()
	if deps.LLMTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deps.LLMTimeout)
		defer cancel()
	}

	suggestion, err := deps.Splitter.DetectSplitPoints(ctx, pages)
	if err != nil {
		return Suggestion{}, err
	}
	return Suggestion{Parts: suggestion.Normalize(pageCount), TextSource: source}, nil
}

// readPageText resolves the text of every page, preferring the PDF's own text
// layer over an OCR call per page — the same born-digital-first choice the
// pipeline's OCR step makes.
func readPageText(
	logger *slog.Logger,
	sourcePath string,
	pageCount int,
	deps DetectDeps,
	report func(done, total int),
) ([]ai.PageText, string, error) {
	pages := make([]ai.PageText, 0, pageCount)
	withText := 0
	for page := 1; page <= pageCount; page++ {
		text, err := pdftool.PageText(context.Background(), sourcePath, page)
		if err != nil {
			return nil, "", fmt.Errorf("read page %d text: %w", page, err)
		}
		if len(text) >= minPageTextChars {
			withText++
		}
		pages = append(pages, ai.PageText{Page: page, Text: text})
		report(page, pageCount)
	}

	// Counted per page rather than averaged: one text-heavy cover page in front
	// of thirty scanned ones would lift an average over any threshold, and the
	// scanned pages are exactly the ones the model needs read.
	if withText*2 >= pageCount {
		return pages, "pdf", nil
	}

	// A scan: the pages carry no text layer, so each one has to be read.
	if deps.OCR == nil {
		return nil, "", ErrDetectNoOCR
	}
	if pageCount > maxDetectOCRPages {
		return nil, "", ErrDetectTooManyPages
	}

	logger.Info("split detection falling back to OCR",
		"pages", pageCount,
		"provider", deps.OCR.Name(),
	)

	workDir, err := os.MkdirTemp("", "paperless-split-detect-*")
	if err != nil {
		return nil, "", fmt.Errorf("prepare work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	report(0, pageCount)
	for i := range pages {
		text, err := ocrOnePage(sourcePath, workDir, pages[i].Page, deps)
		if err != nil {
			return nil, "", fmt.Errorf("read page %d: %w", pages[i].Page, err)
		}
		pages[i].Text = text
		report(pages[i].Page, pageCount)
	}
	return pages, "ocr", nil
}

// ocrOnePage extracts a single page to its own PDF and runs the OCR provider on
// it, which is how a per-page text list is built from a whole-file provider.
func ocrOnePage(sourcePath, workDir string, page int, deps DetectDeps) (string, error) {
	pagePath := filepath.Join(workDir, fmt.Sprintf("page-%d.pdf", page))
	if err := pdftool.ExtractRange(context.Background(), sourcePath, page, page, pagePath); err != nil {
		return "", err
	}
	defer os.Remove(pagePath)

	ctx := context.Background()
	if deps.OCRTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deps.OCRTimeout)
		defer cancel()
	}
	return deps.OCR.ExtractText(ctx, pagePath, "application/pdf")
}
