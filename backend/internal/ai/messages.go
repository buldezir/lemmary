package ai

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/messages"
	"lemmary/backend/internal/opencode"
)

// The Messages API request carries three fields no caller asks for and every
// caller would rather have: a temperature, an effort, and thinking turned off.
// Which of them a given Claude model accepts depends on its generation --
// temperature was removed after Opus 4.6, effort arrived in 4.5, and the Fable
// family refuses to have thinking disabled at all -- and the catalogue does not
// say. So they are sent hopefully and dropped on refusal, one per retry,
// remembered per model once a retry has actually worked. The same shape as the
// degradations completeChat carries, and for the same reason: a table of model
// names would be wrong by the next release.
const (
	fieldTemperature = "temperature"
	fieldThinking    = "thinking"
	fieldEffort      = "effort"
)

// messagesFields is the order a refusal is matched in, and so the order fields
// are dropped in. Temperature first: it is the one every caller sets.
var messagesFields = []string{fieldTemperature, fieldThinking, fieldEffort}

// usesMessagesAPI reports whether this model is served by Anthropic's Messages
// API: always on the anthropic SDK, and for the part of OpenCode Go's catalogue
// that lives there.
func (c *OpenAIClient) usesMessagesAPI(model string) bool {
	return c.sdk == aiprovider.SDKAnthropic ||
		opencode.Endpoint(c.sdk, model) == opencode.EndpointMessages
}

// prepareMessages takes off what this model has already refused, so only the
// first request of a process pays for finding out, and returns the fields that
// have no counterpart in the OpenAI-shaped parameters.
//
// Both options are Anthropic's alone: the MiniMax and Qwen models OpenCode
// serves here take neither field.
//
//   - Effort low, because Claude's own default is high and this codebase's
//     completions are extraction, search and distillation over documents
//     already in hand, paid for by the token. A caller that asks for more
//     gets it.
//   - Thinking off, because internal/messages converts every reply to an
//     openai.ChatCompletion, which has nowhere to keep a thinking block. A tool
//     loop rebuilds the assistant turn from its text and tool calls, and a
//     replayed turn missing the thinking block it was generated with is
//     refused -- so leaving thinking on would break Deep Research, search and
//     chat on their second request.
//
// ponytail: thinking off rather than carried. Carrying it means a side channel
// for opaque blocks through an intermediate shape with no room for them; if
// answers get worse for want of reasoning, that is the work to do.
func (c *OpenAIClient) prepareMessages(params *openai.ChatCompletionNewParams) messages.Options {
	if c.sdk != aiprovider.SDKAnthropic {
		return messages.Options{}
	}
	model := string(params.Model)
	if messagesFieldRefused(c.baseURL, model, fieldTemperature) {
		params.Temperature = param.Opt[float64]{}
	}
	opts := messages.Options{
		Effort:          strings.TrimSpace(string(params.ReasoningEffort)),
		DisableThinking: !messagesFieldRefused(c.baseURL, model, fieldThinking),
	}
	if opts.Effort == "" {
		opts.Effort = string(shared.ReasoningEffortLow)
	}
	if messagesFieldRefused(c.baseURL, model, fieldEffort) {
		opts.Effort = ""
	}
	return opts
}

// dropRefusedMessagesField takes one field back off the request and names it.
// Empty when the refusal is about something else, or about a field that is not
// on the request to begin with -- in which case there is nothing to retry.
func dropRefusedMessagesField(params *openai.ChatCompletionNewParams, opts *messages.Options, err error) string {
	body := messagesErrorBody(err)
	if body == "" {
		return ""
	}
	// output_config is the object effort lives in, and a refusal may name only
	// the object.
	if strings.Contains(body, "output_config") {
		body += " " + fieldEffort
	}
	for _, field := range messagesFields {
		if !strings.Contains(body, field) {
			continue
		}
		switch field {
		case fieldTemperature:
			if !params.Temperature.Valid() {
				continue
			}
			params.Temperature = param.Opt[float64]{}
		case fieldThinking:
			if !opts.DisableThinking {
				continue
			}
			opts.DisableThinking = false
		case fieldEffort:
			if opts.Effort == "" {
				continue
			}
			opts.Effort = ""
		}
		return field
	}
	return ""
}

// messagesErrorBody is the lowercased body of a 400 from this API, and "" for
// anything else. Only a 400 is read: a 429 or a 500 says nothing about the
// shape of the request, and retrying it a field lighter would hide it.
func messagesErrorBody(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
		return ""
	}
	return strings.ToLower(apiErr.RawJSON())
}

// completeMessages sends the request and walks the ladder above.
func (c *OpenAIClient) completeMessages(ctx context.Context, params openai.ChatCompletionNewParams, extra ...any) (*openai.ChatCompletion, error) {
	opts := c.prepareMessages(&params)
	var dropped []string
	for {
		resp, err := messages.Complete(ctx, c.messages, c.logger, c.sdk, c.baseURL, opts, params, extra...)
		if err == nil {
			c.rememberMessagesDrops(string(params.Model), dropped)
			return resp, nil
		}
		// One attempt per droppable field, and no more: past that the refusal
		// is about something this ladder cannot fix.
		if len(dropped) >= len(messagesFields) {
			return nil, err
		}
		field := dropRefusedMessagesField(&params, &opts, err)
		if field == "" {
			return nil, err
		}
		c.logger.Warn("the Messages API refused a request field; retrying without it",
			"model", params.Model, "field", field)
		dropped = append(dropped, field)
		extra = append(extra, "dropped", field)
	}
}

// completeStreamingMessages is completeMessages for a streamed reply. The retry
// is safe for the same reason it is there: a refused field is refused before
// the first event, so nothing has reached the caller yet -- which the text
// check makes sure of.
func (c *OpenAIClient) completeStreamingMessages(ctx context.Context, params openai.ChatCompletionNewParams, onDelta func(string), extra ...any) (string, openai.CompletionUsage, error) {
	opts := c.prepareMessages(&params)
	var dropped []string
	for {
		text, usage, err := messages.CompleteStreaming(ctx, c.messages, c.logger, c.sdk, c.baseURL, opts, params, onDelta, extra...)
		if err == nil {
			c.rememberMessagesDrops(string(params.Model), dropped)
			return text, usage, nil
		}
		if text != "" || len(dropped) >= len(messagesFields) {
			return text, usage, err
		}
		field := dropRefusedMessagesField(&params, &opts, err)
		if field == "" {
			return text, usage, err
		}
		c.logger.Warn("the Messages API refused a request field; retrying without it",
			"model", params.Model, "field", field)
		dropped = append(dropped, field)
		extra = append(extra, "dropped", field)
	}
}

// rememberMessagesDrops records what this model would not take, and only once a
// request without it has actually worked. Noting it on the refusal instead
// would let one misread error turn a field off for the rest of the process.
func (c *OpenAIClient) rememberMessagesDrops(model string, dropped []string) {
	for _, field := range dropped {
		rememberMessagesField(c.baseURL, model, field)
	}
}
