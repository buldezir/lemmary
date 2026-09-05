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
	"github.com/openai/openai-go/shared"
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

func NewOpenAIClient(sdk, apiKey, model, baseURL, promptVer, resultLanguage string, timeout time.Duration, logger *slog.Logger, extra ...option.RequestOption) *OpenAIClient {
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Timeout: timeout}),
		option.WithRequestTimeout(timeout),
		option.WithMaxRetries(0),
		option.WithMiddleware(aiprovider.SessionMiddleware()),
	}
	// Tests pass RewriteHostMiddleware here so a base URL of opencode.ai still
	// lands on httptest. Production callers pass none.
	opts = append(opts, extra...)
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

// CompleteChat sends a chat completion, and gives a provider that refuses it a
// second chance rather than treating the model as broken. In order: JSON mode
// is dropped if response_format is rejected, reasoning_effort is pinned to
// "none" if the model will not take tools alongside it, temperature falls back
// to the API default, and finally the whole request is translated to the
// Responses API if this endpoint turns out not to serve the model at all. Each
// of the last two is remembered per model and endpoint, so the discovery costs
// one rejected request per process rather than one per call.
func CompleteChat(ctx context.Context, client openai.Client, logger *slog.Logger, sdk, baseURL string, params openai.ChatCompletionNewParams, extra ...any) (*openai.ChatCompletion, error) {
	if logger == nil {
		logger = slog.Default()
	}
	// A model already known to live on the Responses API never touches
	// /chat/completions again.
	if needsResponsesAPI(baseURL, string(params.Model)) {
		resp, err := CompleteViaResponses(ctx, client, logger, sdk, baseURL, params, extra...)
		if err == nil {
			logUsage(logger, string(params.Model), usageOf(resp), extra...)
		}
		return resp, err
	}
	// A model that has already refused tools alongside its default
	// reasoning_effort gets the working value up front. Only tool-carrying
	// requests: pinning "none" on the rest would drop the model's reasoning
	// where nothing asked us to.
	if len(params.Tools) > 0 && needsNoReasoningEffort(baseURL, string(params.Model)) {
		params.ReasoningEffort = shared.ReasoningEffort(reasoningEffortNone)
		extra = append(extra, "reasoning_effort", reasoningEffortNone)
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
	if err == nil {
		rememberChatCompletionsWorked(baseURL, string(params.Model))
		logUsage(logger, string(params.Model), usageOf(resp), extra...)
		return resp, nil
	}
	// JSON mode is a request, not a requirement: the callers that ask for it
	// all parse leniently, so a provider that rejects response_format is
	// asked again in plain text rather than treated as broken.
	if params.ResponseFormat.OfJSONObject != nil && isUnsupportedResponseFormatError(err) {
		logger.Warn("model rejected response_format; retrying without JSON mode",
			"model", params.Model,
			slog.Any("error", err),
		)
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{}
		aiprovider.LogRequest(
			logger,
			sdk,
			http.MethodPost,
			aiprovider.ChatCompletionsURL(baseURL),
			string(params.Model),
			append(extra, "retry", "omit_response_format")...,
		)
		resp, err = client.Chat.Completions.New(ctx, params)
		if err == nil {
			logUsage(logger, string(params.Model), usageOf(resp), extra...)
			return resp, nil
		}
	}
	// Some gpt-5-family models default reasoning_effort server-side and then
	// refuse the request because function tools are present. The refusal names
	// two ways out, and they are not equal: /responses keeps the tools and the
	// reasoning, while reasoning_effort=none keeps the tools by turning the
	// reasoning off. Take the first, and settle for the second only where
	// there is no Responses endpoint to take it to.
	if len(params.Tools) > 0 && isReasoningEffortToolConflictError(err) {
		logger.Warn("model rejected reasoning_effort with function tools; retrying on the Responses API",
			"model", params.Model,
			slog.Any("error", err),
		)
		if viaResponses, respErr := CompleteViaResponses(ctx, client, logger, sdk, baseURL, params, extra...); respErr == nil {
			rememberResponsesAPI(baseURL, string(params.Model))
			logUsage(logger, string(params.Model), usageOf(viaResponses), extra...)
			return viaResponses, nil
		} else {
			logger.Warn("the Responses API could not serve it either; falling back to reasoning_effort=none",
				"model", params.Model,
				slog.Any("error", respErr),
			)
		}
		params.ReasoningEffort = shared.ReasoningEffort(reasoningEffortNone)
		aiprovider.LogRequest(
			logger,
			sdk,
			http.MethodPost,
			aiprovider.ChatCompletionsURL(baseURL),
			string(params.Model),
			append(extra, "retry", "reasoning_effort_none")...,
		)
		resp, err = client.Chat.Completions.New(ctx, params)
		if err == nil {
			// Remembered only now that the value is known to work: a "none"
			// the provider also refuses is not worth pinning.
			rememberNoReasoningEffort(baseURL, string(params.Model))
			rememberChatCompletionsWorked(baseURL, string(params.Model))
			logUsage(logger, string(params.Model), usageOf(resp), extra...)
			return resp, nil
		}
	}
	if params.Temperature.Valid() && isUnsupportedTemperatureError(err) {
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
		resp, err = client.Chat.Completions.New(ctx, params)
		if err == nil {
			rememberChatCompletionsWorked(baseURL, string(params.Model))
			logUsage(logger, string(params.Model), usageOf(resp), extra...)
		}
	}
	// Last resort: the endpoint may simply not serve this model. OpenCode Zen
	// routes gpt-5.6-luna and friends to /responses and answers 500 here for
	// anything at all. Try there once, and keep the original error if that was
	// not the problem -- a Responses error for a provider that has no such
	// endpoint would only mislead.
	if try, remember := shouldTryResponses(err, baseURL, string(params.Model)); try {
		logger.Warn("chat completions refused this model; retrying on the Responses API",
			"model", params.Model,
			"remember", remember,
			slog.Any("error", err),
		)
		if viaResponses, respErr := CompleteViaResponses(ctx, client, logger, sdk, baseURL, params, extra...); respErr == nil {
			if remember {
				rememberResponsesAPI(baseURL, string(params.Model))
			}
			logUsage(logger, string(params.Model), usageOf(viaResponses), extra...)
			return viaResponses, nil
		}
	}
	return resp, err
}

// Usage is what one completion cost in tokens. Cached counts the part of the
// prompt the provider served from its prefix cache, when it reports one; it is
// included in Prompt, not additional to it.
type Usage struct {
	Prompt     int
	Completion int
	Cached     int
}

// Add sums another completion into the total.
func (u *Usage) Add(o Usage) {
	u.Prompt += o.Prompt
	u.Completion += o.Completion
	u.Cached += o.Cached
}

func usageOf(resp *openai.ChatCompletion) Usage {
	if resp == nil {
		return Usage{}
	}
	return usageFrom(resp.Usage)
}

func usageFrom(u openai.CompletionUsage) Usage {
	return Usage{
		Prompt:     int(u.PromptTokens),
		Completion: int(u.CompletionTokens),
		Cached:     int(u.PromptTokensDetails.CachedTokens),
	}
}

// logUsage records what a completion cost next to the request that made it.
// Providers that report no usage produce a line of zeros, which is still
// worth having: it says the provider is not telling us.
func logUsage(logger *slog.Logger, model string, u Usage, extra ...any) {
	args := []any{
		"model", model,
		"prompt_tokens", u.Prompt,
		"cached_tokens", u.Cached,
		"completion_tokens", u.Completion,
	}
	logger.Info("ai completion usage", append(args, extra...)...)
}

// completeStreaming streams a chat completion, handing each content delta to
// onDelta as it arrives, and returns the accumulated text with what it cost.
// Errors come back with whatever text arrived before them, so the caller can
// choose between keeping a partial answer and retrying without streaming.
//
// Usage arrives in a final chunk with no choices, and only when asked for;
// providers that do not implement stream_options simply never send it.
func (c *OpenAIClient) completeStreaming(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	onDelta func(string),
	extra ...any,
) (string, Usage, error) {
	if needsResponsesAPI(c.baseURL, string(params.Model)) {
		return c.completeStreamingViaResponses(ctx, params, onDelta, extra...)
	}
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)}
	aiprovider.LogRequest(
		c.logger,
		c.sdk,
		http.MethodPost,
		aiprovider.ChatCompletionsURL(c.baseURL),
		string(params.Model),
		append(extra, "stream", true)...,
	)

	stream := c.client.Chat.Completions.NewStreaming(ctx, params)
	defer stream.Close()

	var b strings.Builder
	var usage Usage
	for stream.Next() {
		chunk := stream.Current()
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			usage = usageFrom(chunk.Usage)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		delta := chunk.Choices[0].Delta.Content
		if delta == "" {
			continue
		}
		b.WriteString(delta)
		if onDelta != nil {
			onDelta(delta)
		}
	}
	err := stream.Err()
	if err == nil {
		rememberChatCompletionsWorked(c.baseURL, string(params.Model))
		logUsage(c.logger, string(params.Model), usage, append(extra, "stream", true)...)
		return b.String(), usage, nil
	}
	// Same fallback as CompleteChat, but only while nothing has reached the
	// reader yet: once deltas are on the wire, a second stream would replay a
	// different answer over the first.
	if b.Len() == 0 {
		try, remember := shouldTryResponses(err, c.baseURL, string(params.Model))
		if !try {
			return b.String(), usage, err
		}
		c.logger.Warn("chat completions refused this model; retrying the stream on the Responses API",
			"model", params.Model,
			"remember", remember,
			slog.Any("error", err),
		)
		text, respUsage, respErr := c.completeStreamingViaResponses(ctx, params, onDelta, extra...)
		if respErr == nil {
			if remember {
				rememberResponsesAPI(c.baseURL, string(params.Model))
			}
			return text, respUsage, nil
		}
	}
	return b.String(), usage, err
}

func (c *OpenAIClient) PromptVersion() string {
	return c.promptVer
}
