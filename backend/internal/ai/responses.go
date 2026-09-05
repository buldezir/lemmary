package ai

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
	"github.com/openai/openai-go/shared/constant"
	"lemmary/backend/internal/aiprovider"
)

// Some models are served only by the Responses API. OpenCode Zen documents a
// per-model endpoint and routes gpt-5.6-luna, grok-4.6 and the muse-spark
// models there; /chat/completions answers for those with a bare 500. Rather
// than carry a list of model names that goes stale, the request shape stays
// chat-completions-shaped everywhere and is translated here for the models that
// turn out to need it.
//
// Everything below converts in one direction and back: ChatCompletionNewParams
// to ResponseNewParams, and the Response to a *openai.ChatCompletion the
// existing call sites already know how to read. No caller changes.

// responsesModels remembers the models whose completions had to be translated,
// so the discovery costs one rejected request per model per process instead of
// one per agent round.
var responsesModels sync.Map

func rememberResponsesAPI(model string) {
	if key := modelKey(model); key != "" {
		responsesModels.Store(key, struct{}{})
	}
}

func needsResponsesAPI(model string) bool {
	key := modelKey(model)
	if key == "" {
		return false
	}
	_, ok := responsesModels.Load(key)
	return ok
}

// resetResponsesAPI clears what the process has learned. Tests only.
func resetResponsesAPI() {
	responsesModels.Range(func(k, _ any) bool {
		responsesModels.Delete(k)
		return true
	})
}

// isEndpointMismatchError reports whether a failed /chat/completions request
// looks like the endpoint refusing to serve this model at all, rather than
// disliking something we sent. OpenCode answers 500 for a Responses-only model
// even for a bare "say hi"; 401 and 403 are what it returns for the others, on
// a key that works everywhere else. 502/503/504 are deliberately absent: those
// are ordinary transient failures and retrying them on a different endpoint
// would be guessing.
func isEndpointMismatchError(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *openai.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
		http.StatusMethodNotAllowed, http.StatusInternalServerError, http.StatusNotImplemented:
		return true
	}
	return false
}

// CompleteViaResponses runs a chat-completions-shaped request through the
// Responses API and hands back a chat completion.
func CompleteViaResponses(
	ctx context.Context,
	client openai.Client,
	logger *slog.Logger,
	sdk, baseURL string,
	params openai.ChatCompletionNewParams,
	extra ...any,
) (*openai.ChatCompletion, error) {
	if logger == nil {
		logger = slog.Default()
	}
	req, err := responsesParamsFrom(params)
	if err != nil {
		return nil, err
	}
	aiprovider.LogRequest(
		logger,
		sdk,
		http.MethodPost,
		aiprovider.ResponsesURL(baseURL),
		string(params.Model),
		append(extra, "api", "responses")...,
	)
	resp, err := client.Responses.New(ctx, req)
	if err != nil {
		return nil, err
	}
	return chatCompletionFrom(resp), nil
}

// completeStreamingViaResponses is the streaming twin: text deltas arrive as
// response.output_text.delta events, and the usage totals ride on the final
// response.completed event.
func (c *OpenAIClient) completeStreamingViaResponses(
	ctx context.Context,
	params openai.ChatCompletionNewParams,
	onDelta func(string),
	extra ...any,
) (string, Usage, error) {
	req, err := responsesParamsFrom(params)
	if err != nil {
		return "", Usage{}, err
	}
	aiprovider.LogRequest(
		c.logger,
		c.sdk,
		http.MethodPost,
		aiprovider.ResponsesURL(c.baseURL),
		string(params.Model),
		append(extra, "stream", true, "api", "responses")...,
	)

	stream := c.client.Responses.NewStreaming(ctx, req)
	defer stream.Close()

	var b strings.Builder
	var usage Usage
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			delta := event.Delta.OfString
			if delta == "" {
				continue
			}
			b.WriteString(delta)
			if onDelta != nil {
				onDelta(delta)
			}
		case "response.completed", "response.incomplete", "response.failed":
			usage = usageFromResponses(event.Response.Usage)
		}
	}
	err = stream.Err()
	if err == nil {
		logUsage(c.logger, string(params.Model), usage, append(extra, "stream", true, "api", "responses")...)
	}
	return b.String(), usage, err
}

// responsesParamsFrom translates a chat completion request into a Responses
// one. Only the fields this codebase actually sends are carried across; a field
// nobody sets is a field that cannot silently mistranslate.
func responsesParamsFrom(params openai.ChatCompletionNewParams) (responses.ResponseNewParams, error) {
	input, err := responsesInputFrom(params.Messages)
	if err != nil {
		return responses.ResponseNewParams{}, err
	}
	req := responses.ResponseNewParams{
		Model: shared.ResponsesModel(params.Model),
		Input: responses.ResponseNewParamsInputUnion{OfInputItemList: input},
		// The archive keeps its own conversation state and replays the whole
		// thread every round, so there is nothing to gain from the provider
		// storing it -- and a stored copy is a copy we did not ask for.
		Store: openai.Bool(false),
	}
	if params.Temperature.Valid() {
		req.Temperature = params.Temperature
	}
	if params.MaxTokens.Valid() {
		req.MaxOutputTokens = params.MaxTokens
	}
	if params.ResponseFormat.OfJSONObject != nil {
		req.Text = responses.ResponseTextConfigParam{
			Format: responses.ResponseFormatTextConfigUnionParam{
				OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
			},
		}
	}
	for _, tool := range params.Tools {
		fn := responses.FunctionToolParam{
			Name:       tool.Function.Name,
			Parameters: map[string]any(tool.Function.Parameters),
			// The archive's schemas are hand-written guidance rather than
			// contracts, and strict mode rejects several of them outright.
			Strict: openai.Bool(false),
		}
		if tool.Function.Description.Valid() {
			fn.Description = tool.Function.Description
		}
		req.Tools = append(req.Tools, responses.ToolUnionParam{OfFunction: &fn})
	}
	if choice := params.ToolChoice.OfAuto.Or(""); choice != "" {
		req.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptions(choice)),
		}
	}
	return req, nil
}

// responsesInputFrom turns the chat message list into Responses input items.
// The two shapes disagree about tool calls in particular: chat carries them on
// the assistant message and answers them with a tool-role message, while
// Responses makes each one a free-standing item.
func responsesInputFrom(messages []openai.ChatCompletionMessageParamUnion) (responses.ResponseInputParam, error) {
	input := make(responses.ResponseInputParam, 0, len(messages))
	for _, msg := range messages {
		switch {
		case msg.OfSystem != nil:
			input = append(input, responses.ResponseInputItemParamOfMessage(
				msg.OfSystem.Content.OfString.Or(""), responses.EasyInputMessageRoleSystem))
		case msg.OfDeveloper != nil:
			input = append(input, responses.ResponseInputItemParamOfMessage(
				msg.OfDeveloper.Content.OfString.Or(""), responses.EasyInputMessageRoleDeveloper))
		case msg.OfUser != nil:
			item, err := responsesUserItem(*msg.OfUser)
			if err != nil {
				return nil, err
			}
			input = append(input, item)
		case msg.OfAssistant != nil:
			if text := msg.OfAssistant.Content.OfString.Or(""); text != "" {
				input = append(input, responses.ResponseInputItemParamOfMessage(
					text, responses.EasyInputMessageRoleAssistant))
			}
			for _, call := range msg.OfAssistant.ToolCalls {
				input = append(input, responses.ResponseInputItemParamOfFunctionCall(
					call.Function.Arguments, call.ID, call.Function.Name))
			}
		case msg.OfTool != nil:
			input = append(input, responses.ResponseInputItemParamOfFunctionCallOutput(
				msg.OfTool.ToolCallID, msg.OfTool.Content.OfString.Or("")))
		default:
			return nil, fmt.Errorf("responses: unsupported message shape")
		}
	}
	return input, nil
}

// responsesUserItem carries a user message across, including the image and file
// parts LLM OCR sends.
func responsesUserItem(msg openai.ChatCompletionUserMessageParam) (responses.ResponseInputItemUnionParam, error) {
	if msg.Content.OfString.Valid() {
		return responses.ResponseInputItemParamOfMessage(
			msg.Content.OfString.Value, responses.EasyInputMessageRoleUser), nil
	}
	content := make(responses.ResponseInputMessageContentListParam, 0, len(msg.Content.OfArrayOfContentParts))
	for _, part := range msg.Content.OfArrayOfContentParts {
		switch {
		case part.OfText != nil:
			content = append(content, responses.ResponseInputContentParamOfInputText(part.OfText.Text))
		case part.OfImageURL != nil:
			image := responses.ResponseInputImageParam{
				ImageURL: openai.String(part.OfImageURL.ImageURL.URL),
				Detail:   responses.ResponseInputImageDetail(part.OfImageURL.ImageURL.Detail),
			}
			if image.Detail == "" {
				image.Detail = responses.ResponseInputImageDetailAuto
			}
			content = append(content, responses.ResponseInputContentUnionParam{OfInputImage: &image})
		case part.OfFile != nil:
			file := responses.ResponseInputFileParam{
				FileData: part.OfFile.File.FileData,
				FileID:   part.OfFile.File.FileID,
				Filename: part.OfFile.File.Filename,
			}
			content = append(content, responses.ResponseInputContentUnionParam{OfInputFile: &file})
		default:
			return responses.ResponseInputItemUnionParam{}, fmt.Errorf("responses: unsupported user content part")
		}
	}
	return responses.ResponseInputItemParamOfInputMessage(content, string(responses.EasyInputMessageRoleUser)), nil
}

// chatCompletionFrom folds a Response back into the one-choice chat completion
// the call sites read. Reasoning items are dropped: they carry no text we can
// show, and replaying them would mean threading provider-specific state through
// every caller.
func chatCompletionFrom(resp *responses.Response) *openai.ChatCompletion {
	if resp == nil {
		return nil
	}
	var text strings.Builder
	var calls []openai.ChatCompletionMessageToolCall
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" {
					text.WriteString(part.Text)
				}
			}
		case "function_call":
			calls = append(calls, openai.ChatCompletionMessageToolCall{
				ID:   item.CallID,
				Type: constant.ValueOf[constant.Function](),
				Function: openai.ChatCompletionMessageToolCallFunction{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}

	finish := "stop"
	if len(calls) > 0 {
		finish = "tool_calls"
	} else if resp.Status == "incomplete" {
		// The only incomplete reason that maps onto a chat finish_reason; the
		// callers treat anything else as a plain stop anyway.
		finish = "length"
	}

	return &openai.ChatCompletion{
		ID:    resp.ID,
		Model: resp.Model,
		Choices: []openai.ChatCompletionChoice{{
			Index:        0,
			FinishReason: finish,
			Message: openai.ChatCompletionMessage{
				Role:      constant.ValueOf[constant.Assistant](),
				Content:   text.String(),
				ToolCalls: calls,
			},
		}},
		Usage: openai.CompletionUsage{
			PromptTokens:     resp.Usage.InputTokens,
			CompletionTokens: resp.Usage.OutputTokens,
			TotalTokens:      resp.Usage.TotalTokens,
			PromptTokensDetails: openai.CompletionUsagePromptTokensDetails{
				CachedTokens: resp.Usage.InputTokensDetails.CachedTokens,
			},
		},
	}
}

func usageFromResponses(u responses.ResponseUsage) Usage {
	return Usage{
		Prompt:     int(u.InputTokens),
		Completion: int(u.OutputTokens),
		Cached:     int(u.InputTokensDetails.CachedTokens),
	}
}
