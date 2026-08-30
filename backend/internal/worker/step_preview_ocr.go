package worker

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/ocr"
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

	previewFile, err := preview.GenerateFirstPagePNG(state.TmpPath)
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
		// Runes, matching the unit the field is bounded in.
		"chars", utf8.RuneCountInString(ocrText),
	)
	return nil
}

// checkOCRTextFits refuses a result the ocr_text column could not hold.
//
// The last thing between a provider's answer and the column. The page ceiling
// in internal/limits is what normally keeps a document under this, but it
// counts pages with pdfinfo and falls back to one page whenever that cannot
// read the file -- so on a host without poppler, or for a PDF poppler dislikes,
// nothing upstream bounded this at all.
//
// Refused rather than shortened. Half a document's text reads as the whole of
// it everywhere it is used afterwards -- search, duplicate detection, the
// extraction prompt -- and none of those can tell that it was cut. A failed
// step says so.
//
// Runes, because that is the unit PocketBase measures the field in. Without
// this the same document still failed, as a validation_max_text_constraint
// raised from inside app.Save that named neither the provider nor a number.
func checkOCRTextFits(providerName, text string) error {
	runes := utf8.RuneCountInString(text)
	if runes <= models.MaxOCRTextRunes {
		return nil
	}
	return fmt.Errorf("ocr: %s returned %d characters, over the %d a document can store",
		providerName, runes, models.MaxOCRTextRunes)
}

// resolveOCRText extracts text via native parsers for born-digital formats,
// otherwise calls the configured OCR provider.
func resolveOCRText(ctx context.Context, state *StepState, provider ocr.Provider) (text, providerName string, err error) {
	if textextract.Supports(state.MimeType) {
		text, err = textextract.Extract(state.TmpPath, state.MimeType)
		if err != nil {
			return "", "", err
		}
		return text, "native", nil
	}

	ocrCtx, cancel := context.WithTimeout(ctx, state.Cfg.OCRTimeout)
	defer cancel()

	text, err = provider.ExtractText(ocrCtx, state.TmpPath, state.MimeType)
	if err != nil {
		return "", "", err
	}
	return text, provider.Name(), nil
}
