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
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/ocr"
	"lemmary/backend/internal/textextract"
)

const ocrTestMaxFileBytes = 10 * 1024 * 1024

// pickableProvider is deliberately not providerResponse, which carries
// api_key_set and the signed-in ChatGPT account. This list is readable by any
// signed-in user; what the operator pays with is not part of that.
type pickableProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	SDK  string `json:"sdk"`
}

// configuredBinding is what Settings would use for a purpose. The alias rides
// along because a caller showing the default has no list to look the id up in.
type configuredBinding struct {
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	Model        string `json:"model,omitempty"`
}

type pickableProvidersResponse struct {
	Providers []pickableProvider `json:"providers"`
	// Configured is empty when nothing is bound, which is what an instance
	// mid-setup looks like.
	Configured configuredBinding `json:"configured"`
}

type ocrTestResponse struct {
	Provider  string `json:"provider"`
	Text      string `json:"text"`
	CharCount int    `json:"char_count"`
	Duration  string `json:"duration"`
}

// handlePickableProviders answers "which providers could do this" for a model
// picker outside Settings, without admin rights.
//
// fallback is what an unqualified request means per route: ParseModelPurpose
// reads anything it does not recognise as the language model, which is wrong
// for the /ocr/providers path the OCR test page calls with no purpose. The
// configured binding is sorted first, so the picker opens on Settings' choice.
func handlePickableProviders(app core.App, rt *config.Runtime, fallback aiprovider.ModelPurpose) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		purpose := fallback
		if raw := strings.TrimSpace(e.Request.URL.Query().Get("for")); raw != "" {
			purpose = aiprovider.ParseModelPurpose(raw)
		}
		// Which configured pair to report, when the capability alone does not
		// say. Defaults to the one the purpose implies.
		bindingName := e.Request.URL.Query().Get("binding")
		providers, err := aiprovider.List(app)
		if err != nil {
			return writeError(e, 500, "Failed to list providers.")
		}
		cfg := rt.Snapshot().Cfg
		preferredID, preferredModel := preferredBinding(cfg, purpose, bindingName)
		configured := configuredBinding{ProviderID: preferredID, Model: preferredModel}
		out := make([]pickableProvider, 0, len(providers))
		var first *pickableProvider
		rest := make([]pickableProvider, 0, len(providers))
		for _, p := range providers {
			// A local sidecar has an address instead of a key, so skipping on the
			// key alone would hide it from the page an operator opens to check the
			// container works. ServesPurpose is the other half: that sidecar is
			// keyless too and cannot read a document at all.
			if !p.Configured() || !aiprovider.ServesPurpose(p.SDK, purpose) {
				continue
			}
			info := pickableProvider{ID: p.ID, Name: p.Alias, SDK: p.SDK}
			if p.ID == preferredID {
				configured.ProviderName = p.Alias
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
		return writeJSON(e, 200, pickableProvidersResponse{Providers: out, Configured: configured})
	}
}

// preferredBinding takes name to break the tie the capability cannot: chat,
// search and extraction are all language models, and telling Deep Search that
// the chat model answers it would be wrong wherever the two were bound
// separately. Unnamed, the LLM purpose answers with chat.
func preferredBinding(cfg config.Config, purpose aiprovider.ModelPurpose, name string) (providerID, model string) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "search":
		return cfg.SearchProviderID, cfg.SearchModel
	case "extract":
		return cfg.ExtractProviderID, cfg.ExtractModel
	case "chat":
		return cfg.ChatProviderID, cfg.ChatModel
	}
	switch purpose {
	case aiprovider.PurposeOCR:
		return cfg.OCRProviderID, cfg.OCRModel
	case aiprovider.PurposeEmbedding:
		return cfg.EmbeddingProviderID, cfg.EmbeddingModel
	default:
		return cfg.ChatProviderID, cfg.ChatModel
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

		tmpFile, err := os.CreateTemp("", "lemmary-ocr-test-*"+filepath.Ext(header.Filename))
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
