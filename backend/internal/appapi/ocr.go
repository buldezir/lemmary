package appapi

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/ocr"
	"paperless-go/backend/internal/textextract"
)

const ocrTestMaxFileBytes = 10 * 1024 * 1024

type ocrProvidersResponse struct {
	Providers []ocr.ProviderInfo `json:"providers"`
}

type ocrTestResponse struct {
	Provider  string `json:"provider"`
	Text      string `json:"text"`
	CharCount int    `json:"char_count"`
	Duration  string `json:"duration"`
}

func ocrProviderConfig(cfg config.Config) ocr.ProviderConfig {
	return ocr.ProviderConfig{
		GoogleVisionAPIKey: cfg.GoogleVisionAPIKey,
		MistralAPIKey:      cfg.MistralAPIKey,
		MistralModel:       cfg.MistralOCRModel,
		MistralBaseURL:     cfg.MistralAPIBaseURL,
		OCRTimeout:         cfg.OCRTimeout,
	}
}

func handleOCRProviders(rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		cfg := rt.Snapshot().Cfg
		return writeJSON(e, 200, ocrProvidersResponse{
			Providers: ocr.AvailableProviders(ocrProviderConfig(cfg)),
		})
	}
}

func handleOCRTest(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		cfg := rt.Snapshot().Cfg
		providerCfg := ocrProviderConfig(cfg)
		providerCfg.Logger = app.Logger().With("component", "ocr")

		if err := e.Request.ParseMultipartForm(ocrTestMaxFileBytes + (1 << 20)); err != nil {
			return writeError(e, 400, "Invalid multipart form.")
		}

		provider := strings.TrimSpace(e.Request.FormValue("provider"))
		if provider == "" {
			return writeError(e, 400, "Provider is required.")
		}

		file, header, err := e.Request.FormFile("file")
		if err != nil {
			return writeError(e, 400, "File is required.")
		}
		defer file.Close()

		tmpFile, err := os.CreateTemp("", "paperless-ocr-test-*"+filepath.Ext(header.Filename))
		if err != nil {
			return writeError(e, 500, "Failed to prepare upload.")
		}
		tmpPath := tmpFile.Name()
		defer os.Remove(tmpPath)

		written, err := io.Copy(tmpFile, io.LimitReader(file, ocrTestMaxFileBytes+1))
		if closeErr := tmpFile.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			return writeError(e, 500, "Failed to save upload.")
		}
		if written > ocrTestMaxFileBytes {
			return writeError(e, 400, fmt.Sprintf("File exceeds %d byte limit.", ocrTestMaxFileBytes))
		}

		mimeType := ocr.GuessMimeType(header.Filename)
		start := time.Now()

		var text, providerName string
		if textextract.Supports(mimeType) {
			text, err = textextract.Extract(tmpPath, mimeType)
			providerName = "native"
		} else {
			ocrProvider, providerErr := ocr.NewProvider(provider, providerCfg)
			if providerErr != nil {
				return writeError(e, 400, providerErr.Error())
			}

			ctx, cancel := context.WithTimeout(e.Request.Context(), cfg.OCRTimeout)
			defer cancel()

			text, err = ocrProvider.ExtractText(ctx, tmpPath, mimeType)
			providerName = ocrProvider.Name()
		}
		if err != nil {
			return writeError(e, 500, err.Error())
		}

		return writeJSON(e, 200, ocrTestResponse{
			Provider:  providerName,
			Text:      text,
			CharCount: len(text),
			Duration:  time.Since(start).Round(time.Millisecond).String(),
		})
	}
}
