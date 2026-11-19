package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/metrics"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/ocr"
	"lemmary/backend/internal/pdftool"
	"lemmary/backend/internal/preview"
	"lemmary/backend/internal/textextract"
)

type PreviewStep struct{}

func (s *PreviewStep) Name() string { return models.StepPreview }

func (s *PreviewStep) ShouldSkip(state *StepState) (bool, error) {
	if state.MimeType != "application/pdf" {
		return true, nil
	}
	if state.forced(models.StepPreview) {
		return false, nil
	}
	if state.Document.GetString("preview") != "" {
		return true, nil
	}
	return false, nil
}

func (s *PreviewStep) Run(ctx context.Context, state *StepState) error {
	_ = ctx
	if err := ensureTempFile(state); err != nil {
		return err
	}
	if state.MimeType != "application/pdf" {
		return nil
	}

	previewFile, err := preview.GenerateFirstPagePNG(ctx, state.TmpPath)
	if err != nil {
		return err
	}

	state.Document.Set("preview", previewFile)
	if err := state.App.Save(state.Document); err != nil {
		return fmt.Errorf("save preview: %w", err)
	}

	state.Logger.Info("preview saved", "file", previewFile.Name)
	return nil
}

type OCRStep struct {
	Provider ocr.Provider
}

func (s *OCRStep) Name() string { return models.StepOCR }

func (s *OCRStep) ShouldSkip(state *StepState) (bool, error) {
	if state.forced(models.StepOCR) {
		return false, nil
	}
	if strings.TrimSpace(state.Document.GetString("ocr_text")) != "" {
		state.OCRText = strings.TrimSpace(state.Document.GetString("ocr_text"))
		return true, nil
	}
	return false, nil
}

func (s *OCRStep) Run(ctx context.Context, state *StepState) error {
	if err := ensureTempFile(state); err != nil {
		return err
	}

	ocrText, providerName, err := resolveOCRText(ctx, state, s.Provider)
	if err != nil {
		return fmt.Errorf("ocr: %w", err)
	}

	if err := checkOCRTextFits(providerName, ocrText); err != nil {
		return err
	}

	state.OCRText = ocrText
	state.Document.Set("ocr_text", ocrText)
	if err := state.App.Save(state.Document); err != nil {
		return fmt.Errorf("save ocr text: %w", err)
	}

	state.Logger.Info("OCR complete",
		"provider", providerName,
		"mime", state.MimeType,
		"chars", utf8.RuneCountInString(ocrText),
	)
	return nil
}

// The last thing between a provider's answer and the ocr_text column. The page
// ceiling in internal/limits normally keeps a document under it, but that falls
// back to one page whenever pdfinfo cannot read the file, so on a host without
// poppler nothing upstream bounds this at all.
//
// Refused rather than shortened: half a document's text reads as the whole of
// it in search, duplicate detection and the extraction prompt alike.
//
// Runes, because that is the unit PocketBase measures the field in.
func checkOCRTextFits(providerName, text string) error {
	runes := utf8.RuneCountInString(text)
	if runes <= models.MaxOCRTextRunes {
		return nil
	}
	return fmt.Errorf("ocr: %s returned %d characters, over the %d a document can store",
		providerName, runes, models.MaxOCRTextRunes)
}

func resolveOCRText(ctx context.Context, state *StepState, provider ocr.Provider) (text, providerName string, err error) {
	if textextract.Supports(state.MimeType) {
		text, err = textextract.Extract(state.TmpPath, state.MimeType)
		if err != nil {
			return "", "", err
		}
		return text, "native", nil
	}

	if state.MimeType == "application/pdf" {
		text, err = ocrPDF(ctx, state, provider)
	} else {
		text, err = extractWithTimeout(ctx, provider, state.TmpPath, state.MimeType, state.Cfg.OCRTimeout)
	}
	if err != nil {
		return "", "", err
	}
	// At least one: a document from before page_count was stamped reads 0.
	metrics.OCRPages(max(1, int64(state.Document.GetFloat("page_count"))))
	return text, provider.Name(), nil
}

func extractWithTimeout(ctx context.Context, provider ocr.Provider, path, mimeType string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return provider.ExtractText(ctx, path, mimeType)
}

// ocrPDF reads a PDF whole, or in parts when OCRPageMode says so: always is
// one page per request, auto halves only what the provider refused as too
// large or over its rate limit. Parts go one after another, never in parallel,
// since parallel requests are what a per-minute limit counts.
func ocrPDF(ctx context.Context, state *StepState, provider ocr.Provider) (string, error) {
	mode := state.Cfg.OCRPageMode
	var wholeErr error
	if mode != config.OCRPageModeAlways {
		if mode != config.OCRPageModeAuto {
			return extractWithTimeout(ctx, provider, state.TmpPath, state.MimeType, state.Cfg.OCRTimeout)
		}
		text, err := extractWaitingOnce(ctx, state, provider, state.TmpPath)
		if !ocr.TooLarge(err) {
			return text, err
		}
		wholeErr = err
	}

	pages, err := pdftool.PageCount(ctx, state.TmpPath)
	if err != nil {
		state.Logger.Warn("ocr: cannot count pages, not splitting", "error", err)
	}
	if err != nil || pages <= 1 {
		if wholeErr != nil {
			return "", wholeErr
		}
		return extractWaitingOnce(ctx, state, provider, state.TmpPath)
	}
	if wholeErr != nil {
		state.Logger.Warn("ocr: provider refused the whole document, splitting it", "pages", pages, "error", wholeErr)
	}
	dir, err := os.MkdirTemp("", "lemmary-ocr-pages-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	var ranges [][2]int
	if mode == config.OCRPageModeAlways {
		for page := 1; page <= pages; page++ {
			ranges = append(ranges, [2]int{page, page})
		}
	} else {
		ranges = halves(1, pages)
	}
	var texts []string
	var emptyErr error
	for len(ranges) > 0 {
		from, to := ranges[0][0], ranges[0][1]
		ranges = ranges[1:]
		text, err := readPages(ctx, state, provider, dir, from, to)
		switch {
		case err == nil:
			texts = append(texts, text)
		case errors.Is(err, ocr.ErrEmptyText):
			emptyErr = err
		case !ocr.TooLarge(err) || from == to:
			return "", err
		default:
			ranges = append(halves(from, to), ranges...)
		}
	}
	if len(texts) == 0 {
		return "", emptyErr
	}
	return strings.Join(texts, "\n\n"), nil
}

func halves(from, to int) [][2]int {
	mid := (from + to) / 2
	return [][2]int{{from, mid}, {mid + 1, to}}
}

// extractWaitingOnce sends a PDF and, after a rate-limit refusal, waits as long
// as the provider asked and sends it once more: by then the per-minute budget
// spent before it has come back.
func extractWaitingOnce(ctx context.Context, state *StepState, provider ocr.Provider, path string) (string, error) {
	text, err := extractWithTimeout(ctx, provider, path, "application/pdf", state.Cfg.OCRTimeout)
	wait, limited := ocr.RetryAfter(err)
	if !limited {
		return text, err
	}
	if ocrRateLimitSleep(ctx, wait); ctx.Err() != nil {
		return "", ctx.Err()
	}
	return extractWithTimeout(ctx, provider, path, "application/pdf", state.Cfg.OCRTimeout)
}

// readPages sends pages from..to as one request.
func readPages(ctx context.Context, state *StepState, provider ocr.Provider, dir string, from, to int) (string, error) {
	path := filepath.Join(dir, fmt.Sprintf("pages-%d-%d.pdf", from, to))
	if err := pdftool.ExtractRange(ctx, state.TmpPath, from, to, path); err != nil {
		return "", err
	}
	text, err := extractWaitingOnce(ctx, state, provider, path)
	if err != nil && from == to {
		return "", fmt.Errorf("page %d: %w", from, err)
	}
	if err != nil {
		return "", fmt.Errorf("pages %d-%d: %w", from, to, err)
	}
	return strings.TrimSpace(text), nil
}

var ocrRateLimitSleep = ai.SleepCtx
