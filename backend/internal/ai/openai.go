package ai

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"lemmary/backend/internal/aiprovider"
)

type OpenAIClient struct {
	sdk            string
	apiKey         string
	model          string
	baseURL        string
	promptVer      string
	resultLanguage string
	client         openai.Client
	logger         *slog.Logger
}

func NewOpenAIClient(sdk, apiKey, model, baseURL, promptVer, resultLanguage string, timeout time.Duration, logger *slog.Logger) *OpenAIClient {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Timeout: timeout}),
		option.WithRequestTimeout(timeout),
		option.WithMaxRetries(0),
	}
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, option.WithBaseURL(strings.TrimRight(baseURL, "/")))
	}
	if strings.TrimSpace(sdk) == "" {
		sdk = "openai"
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &OpenAIClient{
		sdk:            sdk,
		apiKey:         apiKey,
		model:          model,
		baseURL:        strings.TrimRight(baseURL, "/"),
		promptVer:      promptVer,
		resultLanguage: resultLanguage,
		client:         openai.NewClient(opts...),
		logger:         logger,
	}
}

func (c *OpenAIClient) Name() string {
	return c.sdk
}

func (c *OpenAIClient) Model() string {
	return c.model
}

func (c *OpenAIClient) complete(ctx context.Context, params openai.ChatCompletionNewParams, extra ...any) (*openai.ChatCompletion, error) {
	return CompleteChat(ctx, c.client, c.logger, c.sdk, c.baseURL, params, extra...)
}

// CompleteChat sends a chat completion and retries once without temperature
// if the model rejects a custom value.
func CompleteChat(ctx context.Context, client openai.Client, logger *slog.Logger, sdk, baseURL string, params openai.ChatCompletionNewParams, extra ...any) (*openai.ChatCompletion, error) {
	if logger == nil {
		logger = slog.Default()
	}
	aiprovider.LogRequest(
		logger,
		sdk,
		http.MethodPost,
		aiprovider.ChatCompletionsURL(baseURL),
		string(params.Model),
		extra...,
	)
	resp, err := client.Chat.Completions.New(ctx, params)
	if err != nil && params.Temperature.Valid() && isUnsupportedTemperatureError(err) {
		logger.Warn("model rejected temperature; retrying with API default",
			"model", params.Model,
			slog.Any("error", err),
		)
		params.Temperature = param.Opt[float64]{}
		aiprovider.LogRequest(
			logger,
			sdk,
			http.MethodPost,
			aiprovider.ChatCompletionsURL(baseURL),
			string(params.Model),
			append(extra, "retry", "omit_temperature")...,
		)
		return client.Chat.Completions.New(ctx, params)
	}
	return resp, err
}

func (c *OpenAIClient) PromptVersion() string {
	return c.promptVer
}
