package worker

import (
	"context"
	"fmt"
	"strings"

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

	state.OCRText = ocrText
	state.Document.Set("ocr_text", ocrText)
	if err := state.App.Save(state.Document); err != nil {
		return fmt.Errorf("save ocr text: %w", err)
	}

	state.Logger.Info("OCR complete",
		"provider", providerName,
		"mime", state.MimeType,
		"chars", len(ocrText),
	)
	return nil
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
