package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared/constant"

	"lemmary/backend/internal/aiprovider"
)

// The models on EndpointMessages speak Anthropic's Messages API. Everything
// below converts openai.ChatCompletionNewParams to anthropic.MessageNewParams
// and the reply back, so no call site changes. Only the fields this codebase
// actually sends are carried across.

// defaultMaxTokens caps a Messages reply. The parameter is required there and
// optional on /chat/completions, so no caller sets one and it has to be
// invented; generous enough for a Deep Search distil, and only spent if the
// model writes it.
//
// ponytail: one number for every purpose. If a caller ever needs a different
// ceiling, thread it through openai.ChatCompletionNewParams.MaxTokens, which
// responsesParamsFrom already honours and this already prefers.
const defaultMaxTokens = 32768

// NewMessages builds the Anthropic client for an OpenCode base URL. Both
// credentials are sent: x-api-key, which @ai-sdk/anthropic presents, and an
// Authorization bearer, which OpenCode's other endpoints take. The docs
// specify neither and one header costs nothing.
func NewMessages(apiKey, baseURL string, timeout time.Duration) anthropic.Client {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	opts := []anthropicoption.RequestOption{
		// Without this marker the SDK also walks its own credential chain
		// (ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL, a config profile), so a
		// developer with Claude configured on the same host would have it
		// contribute to these requests.
		anthropicoption.WithoutEnvironmentDefaults(),
		anthropicoption.WithAPIKey(apiKey),
		anthropicoption.WithAuthToken(apiKey),
		anthropicoption.WithBaseURL(MessagesBaseURL(baseURL)),
		anthropicoption.WithHTTPClient(&http.Client{Timeout: timeout}),
		anthropicoption.WithRequestTimeout(timeout),
		anthropicoption.WithMaxRetries(0),
		anthropicoption.WithHeader("User-Agent", aiprovider.UserAgent),
		anthropicoption.WithMiddleware(sessionMiddleware()),
	}
	if aiprovider.Managed() {
		opts = append(opts, anthropicoption.WithMiddleware(documentMiddleware))
	}
	return anthropic.NewClient(opts...)
}

// documentMiddleware is the Anthropic SDK's copy of the document-header
// middleware aiprovider.DocumentOptions installs on the OpenAI one: same
// header, same context value, but the two SDKs cannot share a middleware.
func documentMiddleware(req *http.Request, next anthropicoption.MiddlewareNext) (*http.Response, error) {
	aiprovider.StampDocument(req)
	return next(req)
}

// sessionMiddleware is aiprovider.SessionMiddleware for the Anthropic SDK,
// whose option package is its own type.
func sessionMiddleware() anthropicoption.Middleware {
	return func(req *http.Request, next anthropicoption.MiddlewareNext) (*http.Response, error) {
		if req != nil {
			if id := aiprovider.SessionFrom(req.Context()); id != "" {
				req.Header.Set(aiprovider.SessionHeader, id)
			}
		}
		return next(req)
	}
}

// MessagesBaseURL is the base URL to build the Anthropic client with.
// anthropic-sdk-go appends "v1/messages" itself, so the /v1 every other
// endpoint's base URL carries has to come off first, or the request goes to
// /zen/go/v1/v1/messages.
func MessagesBaseURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = strings.TrimRight(aiprovider.DefaultBaseURL(aiprovider.SDKOpenCode), "/")
	}
	return strings.TrimSuffix(base, "/v1") + "/"
}

// MessagesURL is the /messages endpoint for an OpenCode base URL. It exists
// for the outbound request log: the SDK builds the real URL itself, and a log
// line that guessed a different one would be worse than none.
func MessagesURL(baseURL string) string {
	return MessagesBaseURL(baseURL) + "v1/messages"
}

func CompleteViaMessages(
	ctx context.Context,
	client anthropic.Client,
	logger *slog.Logger,
	baseURL string,
	params openai.ChatCompletionNewParams,
	extra ...any,
) (*openai.ChatCompletion, error) {
	if logger == nil {
		logger = slog.Default()
	}
	req, err := messagesParamsFrom(params)
	if err != nil {
		return nil, err
	}
	aiprovider.LogRequest(
		logger,
		aiprovider.SDKOpenCode,
		http.MethodPost,
		MessagesURL(baseURL),
		string(params.Model),
		append(extra, "api", "messages")...,
	)
	msg, err := client.Messages.New(ctx, req)
	if err != nil {
		return nil, err
	}
	return chatCompletionFrom(msg), nil
}

// CompleteStreamingViaMessages returns openai.CompletionUsage rather than
// internal/ai's Usage because this package is a leaf that internal/ai imports.
func CompleteStreamingViaMessages(
	ctx context.Context,
	client anthropic.Client,
	logger *slog.Logger,
	baseURL string,
	params openai.ChatCompletionNewParams,
	onDelta func(string),
	extra ...any,
) (string, openai.CompletionUsage, error) {
	if logger == nil {
		logger = slog.Default()
	}
	var usage openai.CompletionUsage
	req, err := messagesParamsFrom(params)
	if err != nil {
		return "", usage, err
	}
	aiprovider.LogRequest(
		logger,
		aiprovider.SDKOpenCode,
		http.MethodPost,
		MessagesURL(baseURL),
		string(params.Model),
		append(extra, "stream", true, "api", "messages")...,
	)

	stream := client.Messages.NewStreaming(ctx, req)
	defer stream.Close()

	var b strings.Builder
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "message_start":
			usage = usageFrom(event.Message.Usage)
		case "content_block_delta":
			// text_delta only: a thinking_delta carries no answer text, and an
			// input_json_delta belongs to a tool call the streaming call sites do
			// not send.
			delta := event.Delta.Text
			if delta == "" {
				continue
			}
			b.WriteString(delta)
			if onDelta != nil {
				onDelta(delta)
			}
		case "message_delta":
			// Cumulative, and the last word on what the request cost.
			usage = openai.CompletionUsage{
				PromptTokens:     event.Usage.InputTokens,
				CompletionTokens: event.Usage.OutputTokens,
				TotalTokens:      event.Usage.InputTokens + event.Usage.OutputTokens,
				PromptTokensDetails: openai.CompletionUsagePromptTokensDetails{
					CachedTokens: event.Usage.CacheReadInputTokens,
				},
			}
		}
	}
	return b.String(), usage, stream.Err()
}

func messagesParamsFrom(params openai.ChatCompletionNewParams) (anthropic.MessageNewParams, error) {
	system, messages, err := messagesFrom(params.Messages)
	if err != nil {
		return anthropic.MessageNewParams{}, err
	}
	req := anthropic.MessageNewParams{
		Model:     anthropic.Model(params.Model),
		Messages:  messages,
		System:    system,
		MaxTokens: defaultMaxTokens,
	}
	if params.MaxTokens.Valid() {
		req.MaxTokens = params.MaxTokens.Value
	}
	if params.Temperature.Valid() {
		// The two APIs scale it differently: chat completions takes 0-2,
		// Messages takes 0-1.
		req.Temperature = anthropic.Float(min(params.Temperature.Value, 1))
	}
	// params.ResponseFormat is deliberately dropped: the Messages API has no bare
	// "give me JSON" mode, and every caller here already asks for JSON in its own
	// prompt and parses leniently through models.NormalizeJSONObject.
	for _, tool := range params.Tools {
		fn := anthropic.ToolParam{
			Name:        tool.Function.Name,
			InputSchema: toolInputSchemaFrom(tool.Function.Parameters),
		}
		if tool.Function.Description.Valid() {
			fn.Description = anthropic.String(tool.Function.Description.Value)
		}
		req.Tools = append(req.Tools, anthropic.ToolUnionParam{OfTool: &fn})
	}
	if choice := params.ToolChoice.OfAuto.Or(""); choice != "" {
		switch choice {
		case "none":
			req.ToolChoice = anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
		case "required":
			req.ToolChoice = anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
		default:
			req.ToolChoice = anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
		}
	}
	return req, nil
}

// toolInputSchemaFrom carries a function's JSON schema across. Only properties
// and required are named fields there; anything else rides in ExtraFields, so a
// hand-written schema is not quietly trimmed.
func toolInputSchemaFrom(schema map[string]any) anthropic.ToolInputSchemaParam {
	out := anthropic.ToolInputSchemaParam{}
	extras := map[string]any{}
	for key, value := range schema {
		switch key {
		case "type":
			// Always "object"; the field is a constant on the param.
		case "properties":
			out.Properties = value
		case "required":
			out.Required = stringsFrom(value)
		default:
			extras[key] = value
		}
	}
	if len(extras) > 0 {
		out.ExtraFields = extras
	}
	return out
}

func stringsFrom(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// messagesFrom splits the chat message list into the top-level system prompt
// and the conversation. The two shapes disagree twice: there is no system role
// in Messages, and a tool call is a content block on the turn answered by a
// user turn carrying tool_result, rather than a message with its own role.
func messagesFrom(messages []openai.ChatCompletionMessageParamUnion) ([]anthropic.TextBlockParam, []anthropic.MessageParam, error) {
	var system []anthropic.TextBlockParam
	out := make([]anthropic.MessageParam, 0, len(messages))
	for _, msg := range messages {
		switch {
		// Developer messages join the system prompt: Messages has no separate
		// notion of them, and dropping one would drop instructions.
		case msg.OfSystem != nil:
			if text := msg.OfSystem.Content.OfString.Or(""); text != "" {
				system = append(system, anthropic.TextBlockParam{Text: text})
			}
		case msg.OfDeveloper != nil:
			if text := msg.OfDeveloper.Content.OfString.Or(""); text != "" {
				system = append(system, anthropic.TextBlockParam{Text: text})
			}
		case msg.OfUser != nil:
			blocks, err := userBlocksFrom(*msg.OfUser)
			if err != nil {
				return nil, nil, err
			}
			out = appendBlocks(out, anthropic.MessageParamRoleUser, blocks...)
		case msg.OfAssistant != nil:
			var blocks []anthropic.ContentBlockParamUnion
			if text := msg.OfAssistant.Content.OfString.Or(""); text != "" {
				blocks = append(blocks, anthropic.NewTextBlock(text))
			}
			for _, call := range msg.OfAssistant.ToolCalls {
				// Arguments arrive as a JSON string; the block wants the value.
				var input any
				if err := json.Unmarshal([]byte(call.Function.Arguments), &input); err != nil {
					input = call.Function.Arguments
				}
				blocks = append(blocks, anthropic.NewToolUseBlock(call.ID, input, call.Function.Name))
			}
			if len(blocks) == 0 {
				continue
			}
			out = appendBlocks(out, anthropic.MessageParamRoleAssistant, blocks...)
		case msg.OfTool != nil:
			out = appendBlocks(out, anthropic.MessageParamRoleUser,
				anthropic.NewToolResultBlock(msg.OfTool.ToolCallID, msg.OfTool.Content.OfString.Or(""), false))
		default:
			return nil, nil, fmt.Errorf("messages: unsupported message shape")
		}
	}
	return system, out, nil
}

// appendBlocks adds blocks under role, merging them into the previous turn
// when that turn has the same role. The Messages API alternates user and
// assistant turns and a chat completion list does not; behind OpenCode's
// translating gateway a tool_use left unanswered in the turn that immediately
// follows it is a 400. Order within the merged turn is preserved, which keeps
// the tool_result blocks ahead of the instruction after them.
func appendBlocks(out []anthropic.MessageParam, role anthropic.MessageParamRole, blocks ...anthropic.ContentBlockParamUnion) []anthropic.MessageParam {
	if len(blocks) == 0 {
		return out
	}
	if n := len(out); n > 0 && out[n-1].Role == role {
		out[n-1].Content = append(out[n-1].Content, blocks...)
		return out
	}
	return append(out, anthropic.MessageParam{Role: role, Content: blocks})
}

func userBlocksFrom(msg openai.ChatCompletionUserMessageParam) ([]anthropic.ContentBlockParamUnion, error) {
	if msg.Content.OfString.Valid() {
		return []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(msg.Content.OfString.Value)}, nil
	}
	blocks := make([]anthropic.ContentBlockParamUnion, 0, len(msg.Content.OfArrayOfContentParts))
	for _, part := range msg.Content.OfArrayOfContentParts {
		switch {
		case part.OfText != nil:
			blocks = append(blocks, anthropic.NewTextBlock(part.OfText.Text))
		case part.OfImageURL != nil:
			mediaType, data, ok := splitDataURI(part.OfImageURL.ImageURL.URL)
			if !ok {
				// A remote URL, which Messages accepts unlike the file part
				// below.
				blocks = append(blocks, anthropic.NewImageBlock(anthropic.URLImageSourceParam{
					URL: part.OfImageURL.ImageURL.URL,
				}))
				continue
			}
			blocks = append(blocks, anthropic.NewImageBlockBase64(mediaType, data))
		case part.OfFile != nil:
			mediaType, data, ok := splitDataURI(part.OfFile.File.FileData.Or(""))
			if !ok {
				return nil, fmt.Errorf("messages: file part carries no inline data")
			}
			// A PDF is the only document source the Messages API takes as base64;
			// sent as one, a docx would come back as an opaque upstream error.
			if mediaType != "application/pdf" {
				return nil, fmt.Errorf("messages: cannot send %s as a document; use a PDF or an image", mediaType)
			}
			blocks = append(blocks, anthropic.NewDocumentBlock(anthropic.Base64PDFSourceParam{Data: data}))
		default:
			return nil, fmt.Errorf("messages: unsupported user content part")
		}
	}
	return blocks, nil
}

// splitDataURI reads "data:<media type>;base64,<data>" into its two halves.
// Anything else is reported as not one rather than guessed at.
func splitDataURI(uri string) (mediaType, data string, ok bool) {
	if !strings.HasPrefix(uri, "data:") {
		return "", "", false
	}
	header, payload, found := strings.Cut(strings.TrimPrefix(uri, "data:"), ",")
	if !found {
		return "", "", false
	}
	mediaType, encoding, _ := strings.Cut(header, ";")
	if strings.TrimSpace(encoding) != "base64" {
		return "", "", false
	}
	if _, err := base64.StdEncoding.DecodeString(payload); err != nil {
		return "", "", false
	}
	return strings.TrimSpace(mediaType), payload, true
}

// chatCompletionFrom folds a Message back into the one-choice chat completion
// the call sites read. Thinking blocks are dropped: replaying them would mean
// threading provider-specific state through every caller.
func chatCompletionFrom(msg *anthropic.Message) *openai.ChatCompletion {
	if msg == nil {
		return nil
	}
	var text strings.Builder
	var calls []openai.ChatCompletionMessageToolCall
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			calls = append(calls, openai.ChatCompletionMessageToolCall{
				ID:   block.ID,
				Type: constant.ValueOf[constant.Function](),
				Function: openai.ChatCompletionMessageToolCallFunction{
					Name:      block.Name,
					Arguments: string(block.Input),
				},
			})
		}
	}

	finish := "stop"
	switch {
	case len(calls) > 0:
		finish = "tool_calls"
	case msg.StopReason == "max_tokens":
		finish = "length"
	}

	return &openai.ChatCompletion{
		ID:    msg.ID,
		Model: string(msg.Model),
		Choices: []openai.ChatCompletionChoice{{
			Index:        0,
			FinishReason: finish,
			Message: openai.ChatCompletionMessage{
				Role:      constant.ValueOf[constant.Assistant](),
				Content:   text.String(),
				ToolCalls: calls,
			},
		}},
		Usage: usageFrom(msg.Usage),
	}
}

// usageFrom folds Anthropic's token counts into the chat-completions shape.
// cache_read_input_tokens is CachedTokens: part of the input total rather than
// additional to it, the same convention ai.Usage documents.
func usageFrom(u anthropic.Usage) openai.CompletionUsage {
	return openai.CompletionUsage{
		PromptTokens:     u.InputTokens,
		CompletionTokens: u.OutputTokens,
		TotalTokens:      u.InputTokens + u.OutputTokens,
		PromptTokensDetails: openai.CompletionUsagePromptTokensDetails{
			CachedTokens: u.CacheReadInputTokens,
		},
	}
}
