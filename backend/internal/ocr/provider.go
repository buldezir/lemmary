package ocr

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openai/openai-go/option"

	"lemmary/backend/internal/aiprovider"
)

type Provider interface {
	Name() string
	ExtractText(ctx context.Context, filePath string, mimeType string) (string, error)
}

// LimitedConcurrency is implemented by providers that cannot usefully serve
// several requests at once.
//
// The hosted providers all want to be called in parallel: the time is spent on
// the network, so a second request costs nothing while the first is in flight.
// The local sidecar is the opposite -- it is spending this host's CPUs, so a
// second request does not hide latency, it multiplies it, and every caller's
// timeout is already running while it waits for a core.
//
// Callers that fan out ask before choosing a width; see pdfsplit.
type LimitedConcurrency interface {
	MaxConcurrency() int
}

type ProviderInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	SDK  string `json:"sdk"`
}

// NewFromAIProvider builds the OCR provider a row describes.
//
// extra carries request options the caller had to build for it -- today only
// the chatgpt middleware, which mints a bearer token per request because that
// SDK's credential expires hourly and cannot be baked into a client. See
// config.providerCredential.
func NewFromAIProvider(p aiprovider.Provider, model string, timeout time.Duration, logger *slog.Logger, extra ...option.RequestOption) (Provider, error) {
	// Asked first, so an SDK that can never read a document says so instead of
	// complaining about a missing model or key it would have no use for.
	if !aiprovider.CanOCR(p.SDK) {
		return nil, fmt.Errorf("sdk %s cannot read a document; want one of %s", p.SDK, strings.Join(aiprovider.OCRSDKs(), ", "))
	}
	if aiprovider.RequiresAPIKey(p.SDK) && p.APIKey == "" {
		return nil, fmt.Errorf("provider %q has no API key", p.Alias)
	}
	// The local sidecars have an address instead of a credential, and unlike the
	// hosted SDKs there is no public endpoint to fall back on. An empty base URL
	// here would build a client that POSTs to a relative path.
	if aiprovider.RequiresBaseURL(p.SDK) && strings.TrimSpace(p.BaseURL) == "" {
		return nil, fmt.Errorf("provider %q has no base URL", p.Alias)
	}
	if logger == nil {
		logger = slog.Default()
	}
	if aiprovider.RequiresOCRModel(p.SDK) && strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("OCR model is required for sdk %s", p.SDK)
	}

	switch p.SDK {
	case aiprovider.SDKGoogleVision:
		logger.Info("using provider", "provider", p.Alias, "sdk", p.SDK)
		return NewGoogleVisionProvider(p.APIKey, logger), nil
	case aiprovider.SDKMistral:
		logger.Info("using provider", "provider", p.Alias, "sdk", p.SDK, "model", model)
		return NewMistralProvider(p.APIKey, model, p.BaseURL, timeout, logger), nil
	case aiprovider.SDKOpenAI, aiprovider.SDKOpenRouter, aiprovider.SDKOpenCode, aiprovider.SDKChatGPT:
		// chatgpt and opencode join the metered LLM SDKs here rather than getting
		// branches of their own: the request is the same multimodal chat
		// completion, and what differs -- a minted bearer token for one, a second
		// wire protocol for the other -- is entirely inside the client that
		// NewLLMProvider builds.
		logger.Info("using provider", "provider", p.Alias, "sdk", p.SDK, "model", model)
		return NewLLMProvider(p, model, timeout, logger, extra...), nil
	case aiprovider.SDKDocling:
		// model names docling's OCR engine here, not a model; empty leaves the
		// choice to the server. p.APIKey is optional and usually empty.
		logger.Info("using provider", "provider", p.Alias, "sdk", p.SDK, "engine", model, "base_url", p.BaseURL)
		return NewDoclingProvider(p.BaseURL, model, p.APIKey, timeout, logger), nil
	default:
		return nil, fmt.Errorf("unsupported OCR sdk %q", p.SDK)
	}
}
