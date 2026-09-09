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

// pickableProvider is a configured provider as a picker sees it: enough to name
// a row in a dropdown, and nothing else.
//
// Deliberately not providerResponse, which carries api_key_set, the signed-in
// ChatGPT account address and its plan. This list is readable by any signed-in
// user, because any of them may override the model on their own chat or
// reprocess job; what the operator pays with is not part of that.
type pickableProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	SDK  string `json:"sdk"`
}

// configuredBinding is what Settings would use for a purpose, so a picker can
// name the model that answers when nobody overrides anything.
//
// The provider's alias rides along because the id alone is not something to put
// in front of a user, and a caller showing the default has no list to look it
// up in -- an unconfigured binding sends nothing at all.
type configuredBinding struct {
	ProviderID   string `json:"provider_id,omitempty"`
	ProviderName string `json:"provider_name,omitempty"`
	Model        string `json:"model,omitempty"`
}

type pickableProvidersResponse struct {
	Providers []pickableProvider `json:"providers"`
	// Configured is the binding in Settings for the requested purpose. Empty
	// when nothing is bound, which is what an instance mid-setup looks like.
	Configured configuredBinding `json:"configured"`
}

type ocrTestResponse struct {
	Provider  string `json:"provider"`
	Text      string `json:"text"`
	CharCount int    `json:"char_count"`
	Duration  string `json:"duration"`
}

// handlePickableProviders lists the configured providers that can serve a
// purpose, for a model picker outside Settings.
//
// It grew out of the OCR-test page's own list, which is why it is here rather
// than in providers.go: that page needed a non-admin answer to "which providers
// could do this", and so does every provider/model override -- on a chat, on a
// reprocess job. The same handler serves the old /ocr/providers path, where the
// purpose defaults to ocr.
//
// The configured binding for the purpose is sorted first, so the picker opens
// on what Settings would have used anyway.
func handlePickableProviders(app core.App, rt *config.Runtime) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		purpose := aiprovider.ParseModelPurpose(e.Request.URL.Query().Get("for"))
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
			// A local sidecar has an address instead of a key; skipping on the
			// key alone would hide it from the very page an operator opens
			// first to check the container is working. ServesPurpose is the
			// other half: the local embeddings sidecar is configured and
			// keyless too, and cannot read a document at all.
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

// preferredBinding is the configured provider and model a picker opens on, and
// what it reports as answering when nobody overrides anything.
//
// name breaks the tie the capability cannot. Chat, search and extraction are
// all language models, so the purpose alone cannot tell them apart -- and
// telling Deep Search that the chat model answers it would be wrong on any
// instance that bound the two separately. Unnamed, the LLM purpose answers with
// chat: it is the only LLM picker a non-admin reaches without naming one.
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
