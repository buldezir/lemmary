package ai

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/metrics"
	"lemmary/backend/internal/opencode"
)

type OpenAIClient struct {
	sdk            string
	apiKey         string
	model          string
	baseURL        string
	promptVer      string
	resultLanguage string
	// extractionRules is the admin's own additions to the extraction prompt.
	// Only NewExtractor sets it; every other client built here leaves it empty.
	extractionRules string
	client          openai.Client
	logger          *slog.Logger

	// messages is the Anthropic client the opencode SDK needs for the third of
	// its catalogue served on /messages. Never used for any other SDK.
	messages anthropic.Client
}

func NewOpenAIClient(sdk, apiKey, model, baseURL, promptVer, resultLanguage string, timeout time.Duration, logger *slog.Logger, extra ...option.RequestOption) *OpenAIClient {
	if strings.TrimSpace(sdk) == "" {
		sdk = aiprovider.SDKOpenAI
	}
	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
		option.WithHTTPClient(&http.Client{Timeout: timeout}),
		option.WithRequestTimeout(timeout),
		option.WithMaxRetries(0),
	}
	// Only OpenCode asks for the session header.
	if sdk == aiprovider.SDKOpenCode {
		opts = append(opts, option.WithMiddleware(aiprovider.SessionMiddleware()))
	}
	opts = append(opts, aiprovider.UserAgentOptions(sdk)...)
	// Production callers pass the chatgpt middleware here, which mints a bearer
	// token per request; see config.providerCredential.
	opts = append(opts, extra...)
	// Last, so a caller cannot supply a document id of its own: the only id the
	// managed gateway may be told is the one the job or route put on the context.
	opts = append(opts, aiprovider.DocumentOptions()...)
	if strings.TrimSpace(baseURL) != "" {
		opts = append(opts, option.WithBaseURL(strings.TrimRight(baseURL, "/")))
	}
	if logger == nil {
		logger = slog.Default()
	}

	c := &OpenAIClient{
		sdk:            sdk,
		apiKey:         apiKey,
		model:          model,
		baseURL:        strings.TrimRight(baseURL, "/"),
		promptVer:      promptVer,
		resultLanguage: resultLanguage,
		client:         openai.NewClient(opts...),
		logger:         logger,
	}
	if sdk == aiprovider.SDKOpenCode {
		c.messages = opencode.NewMessages(apiKey, baseURL, timeout)
	}
	return c
}

func (c *OpenAIClient) Name() string {
	return c.sdk
}

func (c *OpenAIClient) Model() string {
	return c.model
}

// Complete sends a chat completion, and gives a provider that refuses it a
// second chance rather than treating the model as broken.
//
// opencode.Endpoint decides first whether this model is served somewhere other
// than /chat/completions. Everything after that is degradation on the endpoint
// the model does live on: JSON mode dropped if response_format is rejected,
// reasoning_effort pinned to "none" if the model will not take tools alongside
// it, temperature back to the API default. The reasoning_effort case prefers
// the Responses API, which keeps both, and is remembered per model and
// endpoint.
func (c *OpenAIClient) Complete(ctx context.Context, params openai.ChatCompletionNewParams, extra ...any) (resp *openai.ChatCompletion, err error) {
	// One measurement per call the caller made, not per HTTP request: the
	// endpoint discovery and the degradation retries below are all time it waited.
	defer metrics.TimeAICall(ctx, "chat", c.sdk, string(params.Model))(&err)
	c.markPromptCache(ctx, &params)
	switch opencode.Endpoint(c.sdk, string(params.Model)) {
	case opencode.EndpointMessages:
		resp, err := opencode.CompleteViaMessages(ctx, c.messages, c.logger, c.baseURL, params, extra...)
		if err == nil {
			logUsage(c.logger, string(params.Model), usageOf(resp), extra...)
		}
		return resp, err
	case opencode.EndpointResponses:
		resp, err := CompleteViaResponses(ctx, c.client, c.logger, c.sdk, c.baseURL, params, extra...)
		if err == nil {
			logUsage(c.logger, string(params.Model), usageOf(resp), extra...)
		}
		return resp, err
	}
	return c.completeChat(ctx, params, extra...)
}

func (c *OpenAIClient) completeChat(ctx context.Context, params openai.ChatCompletionNewParams, extra ...any) (*openai.ChatCompletion, error) {
	logger := c.logger
	baseURL := c.baseURL
	// The chatgpt SDK already speaks the Responses API from underneath, and its
	// middleware is the only thing holding the Codex auth headers, so the
	// degradation path below stays on the endpoint that middleware owns.
	viaResponses := !aiprovider.RequiresOAuth(c.sdk)

	// A model already known to live on the Responses API never touches
	// /chat/completions again.
	if viaResponses && needsResponsesAPI(baseURL, string(params.Model)) {
		resp, err := CompleteViaResponses(ctx, c.client, logger, c.sdk, baseURL, params, extra...)
		if err == nil {
			logUsage(logger, string(params.Model), usageOf(resp), extra...)
		}
		return resp, err
	}
	// A model that has already refused tools alongside its default
	// reasoning_effort gets the working value up front. Only tool-carrying
	// requests: pinning "none" on the rest would drop reasoning nothing asked us to.
	if len(params.Tools) > 0 && needsNoReasoningEffort(baseURL, string(params.Model)) {
		params.ReasoningEffort = shared.ReasoningEffort(reasoningEffortNone)
		extra = append(extra, "reasoning_effort", reasoningEffortNone)
	}
	aiprovider.LogRequest(
		logger,
		c.sdk,
		http.MethodPost,
		aiprovider.ChatCompletionsURL(baseURL),
		string(params.Model),
		extra...,
	)
	resp, err := c.client.Chat.Completions.New(ctx, params)
	if err == nil {
		logUsage(logger, string(params.Model), usageOf(resp), extra...)
		return resp, nil
	}
	// JSON mode is a request, not a requirement: the callers that ask for it
	// all parse leniently.
	if params.ResponseFormat.OfJSONObject != nil && isUnsupportedResponseFormatError(err) {
		logger.Warn("model rejected response_format; retrying without JSON mode",
			"model", params.Model,
			slog.Any("error", err),
		)
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{}
		aiprovider.LogRequest(
			logger,
			c.sdk,
			http.MethodPost,
			aiprovider.ChatCompletionsURL(baseURL),
			string(params.Model),
			append(extra, "retry", "omit_response_format")...,
		)
		resp, err = c.client.Chat.Completions.New(ctx, params)
		if err == nil {
			logUsage(logger, string(params.Model), usageOf(resp), extra...)
			return resp, nil
		}
	}
	// Some gpt-5-family models default reasoning_effort server-side and then
	// refuse the request because function tools are present. The two ways out
	// are not equal: /responses keeps the tools and the reasoning, while
	// reasoning_effort=none keeps the tools by turning the reasoning off.
	// refuse the request because function tools are present. The refusal names
	// two ways out, and they are not equal: /responses keeps the tools and the
	// reasoning, while reasoning_effort=none keeps the tools by turning the
	if viaResponses && len(params.Tools) > 0 && isReasoningEffortToolConflictError(err) {
		logger.Warn("model rejected reasoning_effort with function tools; retrying on the Responses API",
			"model", params.Model,
			slog.Any("error", err),
		)
		if viaResponses, respErr := CompleteViaResponses(ctx, c.client, logger, c.sdk, baseURL, params, extra...); respErr == nil {
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
			c.sdk,
			http.MethodPost,
			aiprovider.ChatCompletionsURL(baseURL),
			string(params.Model),
			append(extra, "retry", "reasoning_effort_none")...,
		)
		resp, err = c.client.Chat.Completions.New(ctx, params)
		if err == nil {
			// Remembered only now that the value is known to work: a "none"
			// the provider also refuses is not worth pinning.
			rememberNoReasoningEffort(baseURL, string(params.Model))
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
			c.sdk,
			http.MethodPost,
			aiprovider.ChatCompletionsURL(baseURL),
			string(params.Model),
			append(extra, "retry", "omit_temperature")...,
		)
		resp, err = c.client.Chat.Completions.New(ctx, params)
		if err == nil {
			logUsage(logger, string(params.Model), usageOf(resp), extra...)
		}
	}
	return resp, err
}

// Usage is what one completion cost in tokens. Cached is the part of the prompt
// served from the provider's prefix cache, included in Prompt rather than
// additional to it.
type Usage struct {
	Prompt     int
	Completion int
	Cached     int
}

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

// logUsage records what a completion cost. Providers that report no usage
// produce a line of zeros, which still says the provider is not telling us.
func logUsage(logger *slog.Logger, model string, u Usage, extra ...any) {
	args := []any{
		"model", model,
		"prompt_tokens", u.Prompt,
		"cached_tokens", u.Cached,
		"completion_tokens", u.Completion,
	}
	logger.Info("ai completion usage", append(args, extra...)...)
	metrics.AITokens(model, int64(u.Prompt), int64(u.Cached), int64(u.Completion))
}

// completeStreaming hands each content delta to onDelta as it arrives and
// returns the accumulated text with what it cost. Errors come back with
// whatever text arrived before them, so the caller can keep a partial answer.
// Usage arrives in a final chunk with no choices, and only when asked for.
//
// Only the error return is named, and only so the deferred timer can read it;
// the body already has a `usage` of its own.
func (c *OpenAIClient) completeStreaming(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	onDelta func(string),
	extra ...any,
) (_ string, _ Usage, err error) {
	defer metrics.TimeAICall(ctx, "chat", c.sdk, string(params.Model))(&err)
	c.markPromptCache(ctx, &params)
	switch opencode.Endpoint(c.sdk, string(params.Model)) {
	case opencode.EndpointMessages:
		text, u, err := opencode.CompleteStreamingViaMessages(ctx, c.messages, c.logger, c.baseURL, params, onDelta, extra...)
		usage := usageFrom(u)
		if err == nil {
			logUsage(c.logger, string(params.Model), usage, append(extra, "stream", true, "api", "messages")...)
		}
		return text, usage, err
	case opencode.EndpointResponses:
		return c.completeStreamingViaResponses(ctx, params, onDelta, extra...)
	}
	// See Complete: the chatgpt SDK reaches /responses through its own
	// middleware, the only thing holding the Codex headers.
	if !aiprovider.RequiresOAuth(c.sdk) && needsResponsesAPI(c.baseURL, string(params.Model)) {
		return c.completeStreamingViaResponses(ctx, params, onDelta, extra...)
	}
	// Same pre-correction Complete applies, and for the same reason: a streamed
	// request that carries tools is refused by the same models. There is no
	// degradation path here to learn it, only the one the loop already walked.
	if len(params.Tools) > 0 && needsNoReasoningEffort(c.baseURL, string(params.Model)) {
		params.ReasoningEffort = shared.ReasoningEffort(reasoningEffortNone)
		extra = append(extra, "reasoning_effort", reasoningEffortNone)
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
	err = stream.Err()
	if err == nil {
		logUsage(c.logger, string(params.Model), usage, append(extra, "stream", true)...)
	}
	return b.String(), usage, err
}

func (c *OpenAIClient) PromptVersion() string {
	return c.promptVer
}
