package pdfsplit

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/importjob"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/ocr"
	"lemmary/backend/internal/pdftool"
)

// maxDetectOCRPages bounds the OCR fallback for a scanned file. Each page is a
// separate provider call, so a long scan would take longer than any user is
// willing to wait on a suggestion they can also make by hand.
const maxDetectOCRPages = 40

// minPageTextChars is how much text a page must carry to count as having a text
// layer. A scanned page yields nothing at all, so the bar only has to clear
// stray artifacts.
const minPageTextChars = 16

// detectOCRWorkers bounds how many pages of a scan are sent to the OCR provider
// at once. A handful in flight hides the round trip latency without turning a
// long scan into a burst the provider (or its rate limit) objects to.
const detectOCRWorkers = 4

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
//
// It is held for the length of the run, though. A detection reads the staged PDF
// for as long as it takes to OCR every page, and without the hold a discard, a
// confirmed split or the TTL sweep would delete the file out from under it.
func Detect(app core.App, ownerUserID, uploadID string, deps DetectDeps) (string, error) {
	item, ok := stagingRegistry.Hold(uploadID, ownerUserID)
	if !ok {
		return "", ErrUploadNotFound
	}
	if deps.Splitter == nil {
		stagingRegistry.Unhold(item)
		return "", ErrDetectUnavailable
	}

	logger := app.Logger().With("component", "pdf_split")
	sourcePath, pageCount := sourcePathOf(item), item.Payload.preview.PageCount
	jobID, err := detectRegistry.Start(ownerUserID, func(report func(done, total int)) (Suggestion, error) {
		defer stagingRegistry.Unhold(item)
		return runDetect(logger, sourcePath, pageCount, deps, report)
	})
	if err != nil {
		// The job never started, so the hold ends with it.
		stagingRegistry.Unhold(item)
		return "", err
	}
	return jobID, nil
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
	// One pdftotext run for the whole file: starting it per page would re-parse
	// the document up to MaxPages times over.
	texts, err := pdftool.AllPagesText(context.Background(), sourcePath, pageCount)
	if err != nil {
		return nil, "", fmt.Errorf("read page text: %w", err)
	}

	pages := make([]ai.PageText, 0, pageCount)
	withText := 0
	for page := 1; page <= pageCount; page++ {
		text := texts[page-1]
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

	workDir, err := os.MkdirTemp("", "lemmary-split-detect-*")
	if err != nil {
		return nil, "", fmt.Errorf("prepare work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	report(0, pageCount)
	if err := ocrPages(pages, sourcePath, workDir, deps, report); err != nil {
		return nil, "", err
	}
	return pages, "ocr", nil
}

// ocrPages fills in the text of every page from the OCR provider, a few pages
// at a time.
//
// The calls are network bound and independent, so running them one after another
// made the wall time the sum of up to maxDetectOCRPages round trips. Order is
// preserved because each worker writes only its own slot; the first failure
// cancels the rest.
func ocrPages(
	pages []ai.PageText,
	sourcePath, workDir string,
	deps DetectDeps,
	report func(done, total int),
) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workers := min(detectOCRWorkers, len(pages), providerConcurrency(deps.OCR))
	var (
		next atomic.Int64
		mu   sync.Mutex
		done int
		wg   sync.WaitGroup
	)
	errs := make([]error, len(pages))

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(pages) || ctx.Err() != nil {
					return
				}
				text, err := ocrOnePage(ctx, sourcePath, workDir, pages[i].Page, deps)
				if err != nil {
					errs[i] = fmt.Errorf("read page %d: %w", pages[i].Page, err)
					cancel()
					return
				}
				pages[i].Text = text

				mu.Lock()
				done++
				report(done, len(pages))
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	// Reported by page order, so the message names the first page that failed
	// rather than whichever worker lost the race.
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// providerConcurrency is how many pages this provider wants in flight at once.
//
// detectOCRWorkers is tuned for the hosted providers, where the time is spent
// on the network and four requests cost about what one does. A local sidecar
// spends this host's CPUs instead: four at a time does not overlap, it queues,
// and each page's OCRTimeout is already counting down while it waits its turn.
// Providers that know this say so by implementing ocr.LimitedConcurrency; the
// rest keep the fan-out they have always had.
func providerConcurrency(provider ocr.Provider) int {
	limited, ok := provider.(ocr.LimitedConcurrency)
	if !ok {
		return detectOCRWorkers
	}
	return max(1, limited.MaxConcurrency())
}

// ocrOnePage extracts a single page to its own PDF and runs the OCR provider on
// it, which is how a per-page text list is built from a whole-file provider.
func ocrOnePage(ctx context.Context, sourcePath, workDir string, page int, deps DetectDeps) (string, error) {
	pagePath := filepath.Join(workDir, fmt.Sprintf("page-%d.pdf", page))
	if err := pdftool.ExtractRange(ctx, sourcePath, page, page, pagePath); err != nil {
		return "", err
	}
	defer os.Remove(pagePath)

	if deps.OCRTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, deps.OCRTimeout)
		defer cancel()
	}
	return deps.OCR.ExtractText(ctx, pagePath, "application/pdf")
}
