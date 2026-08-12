package ocr

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"paperless-go/backend/internal/aiprovider"
)

type Provider interface {
	Name() string
	ExtractText(ctx context.Context, filePath string, mimeType string) (string, error)
}

type ProviderInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	SDK  string `json:"sdk"`
}

func NewFromAIProvider(p aiprovider.Provider, model string, timeout time.Duration, logger *slog.Logger) (Provider, error) {
	if p.APIKey == "" {
		return nil, fmt.Errorf("provider %q has no API key", p.Alias)
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
	case aiprovider.SDKOpenAI, aiprovider.SDKOpenRouter:
		logger.Info("using provider", "provider", p.Alias, "sdk", p.SDK, "model", model)
		return NewLLMProvider(p, model, timeout, logger), nil
	default:
		return nil, fmt.Errorf("unsupported OCR sdk %q", p.SDK)
	}
}
