package ocr

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openai/openai-go/option"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/metrics"
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

// NewFromAIProvider builds the OCR provider a row describes.
//
// extra carries request options the caller had to build for it -- today only
// the chatgpt middleware, which mints a bearer token per request because that
// SDK's credential expires hourly and cannot be baked into a client. See
// config.providerCredential.
func NewFromAIProvider(p aiprovider.Provider, model string, timeout time.Duration, logger *slog.Logger, extra ...option.RequestOption) (Provider, error) {
	provider, err := newProvider(p, model, timeout, logger, extra...)
	if err != nil {
		return nil, err
	}
	// One wrap for all four backends, Google Vision included -- that one talks
	// gRPC, where no HTTP instrumentation of ours could ever have seen it.
	return withMetrics(provider, p.SDK, model), nil
}

func newProvider(p aiprovider.Provider, model string, timeout time.Duration, logger *slog.Logger, extra ...option.RequestOption) (Provider, error) {
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

// timed reports how long one text extraction took, and whether it failed.
// Provider is embedded, so Name passes through untouched.
//
// The LLM backend reads a document by sending a chat completion, so it records
// twice -- once here as ocr, once inside ai.Complete as chat. That is one
// extraction and one completion, not one thing counted twice; do not sum the
// two kinds.
type timed struct {
	Provider
	sdk   string
	model string
}

func (t timed) ExtractText(ctx context.Context, filePath string, mimeType string) (_ string, err error) {
	defer metrics.TimeAICall(ctx, "ocr", t.sdk, t.model)(&err)
	return t.Provider.ExtractText(ctx, filePath, mimeType)
}

// timedLimited is timed for a provider that also caps its own concurrency.
// Embedding Provider alone would hide MaxConcurrency from the type assertion
// in pdfsplit and quietly widen the fan-out onto a sidecar that asked for one
// request at a time; a second type is smaller than that bug.
type timedLimited struct {
	timed
	LimitedConcurrency
}

func withMetrics(p Provider, sdk, model string) Provider {
	t := timed{Provider: p, sdk: sdk, model: model}
	if limited, ok := p.(LimitedConcurrency); ok {
		return timedLimited{timed: t, LimitedConcurrency: limited}
	}
	return t
}
