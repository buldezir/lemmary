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
	"paperless-go/backend/internal/aiprovider"
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

func handleOCRProviders(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		providers, err := aiprovider.List(app)
		if err != nil {
			return writeError(e, 500, "Failed to list OCR providers.")
		}
		preferred := rt.Snapshot().Cfg.OCRProviderID
		out := make([]ocr.ProviderInfo, 0, len(providers))
		var first *ocr.ProviderInfo
		rest := make([]ocr.ProviderInfo, 0, len(providers))
		for _, p := range providers {
			if p.APIKey == "" {
				continue
			}
			info := ocr.ProviderInfo{ID: p.ID, Name: p.Alias, SDK: p.SDK}
			if p.ID == preferred {
				copy := info
				first = &copy
				continue
			}
			rest = append(rest, info)
		}
		if first != nil {
			out = append(out, *first)
		}
		out = append(out, rest...)
		return writeJSON(e, 200, ocrProvidersResponse{Providers: out})
	}
}

func handleOCRTest(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		cfg := rt.Snapshot().Cfg

		if err := e.Request.ParseMultipartForm(ocrTestMaxFileBytes + (1 << 20)); err != nil {
			return writeError(e, 400, "Invalid multipart form.")
		}

		providerID := strings.TrimSpace(e.Request.FormValue("provider"))
		if providerID == "" {
			return writeError(e, 400, "Provider is required.")
		}
		model := strings.TrimSpace(e.Request.FormValue("model"))

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
			p, findErr := aiprovider.FindByID(app, providerID)
			if findErr != nil || p == nil {
				return writeError(e, 400, "Unknown OCR provider.")
			}
			if model == "" && cfg.OCRProviderID == p.ID {
				model = cfg.OCRModel
			}
			ocrProvider, providerErr := ocr.NewFromAIProvider(*p, model, cfg.OCRTimeout, app.Logger().With("component", "ocr"))
			if providerErr != nil {
				return writeError(e, 400, providerErr.Error())
			}

			ctx, cancel := context.WithTimeout(e.Request.Context(), cfg.OCRTimeout)
			defer cancel()

			text, err = ocrProvider.ExtractText(ctx, tmpPath, mimeType)
			providerName = ocrProvider.Name()
		}
		if err != nil {
			// Extraction errors can embed server paths and raw upstream response
			// bodies; log the detail, return a generic message.
			app.Logger().Error("ocr test failed", "provider", providerName, "error", err)
			return writeError(e, 500, "OCR extraction failed; check the server logs for details.")
		}

		return writeJSON(e, 200, ocrTestResponse{
			Provider:  providerName,
			Text:      text,
			CharCount: len(text),
			Duration:  time.Since(start).Round(time.Millisecond).String(),
		})
	}
}
