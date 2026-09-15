package ai

import (
	"context"

	"github.com/openai/openai-go"

	"lemmary/backend/internal/aiprovider"
)

// markPromptCache tells the provider that this request shares a prefix with the
// last one, so it can reuse the work it already did instead of reading the
// whole conversation again.
//
// Two dialects, because the providers do not agree. OpenAI finds the prefix
// itself and only wants a key to group the requests by. Anthropic-shaped
// providers cache nothing unless a cache_control breakpoint says where the
// reusable part ends, and OpenRouter passes such a breakpoint through to the
// model behind it.
//
// The other SDKs are left alone deliberately: OpenCode Go routes by the
// x-opencode-session header it already gets, its /messages endpoint is marked
// in that SDK's own types, and the rest either cache unasked or reject a field
// they do not know.
func (c *OpenAIClient) markPromptCache(ctx context.Context, params *openai.ChatCompletionNewParams) {
	switch c.sdk {
	case aiprovider.SDKOpenAI:
		if id := aiprovider.SessionFrom(ctx); id != "" {
			params.PromptCacheKey = openai.String(id)
		}
	case aiprovider.SDKOpenRouter:
		params.Messages = withCacheBreakpoint(params.Messages)
	}
}

// withCacheBreakpoint marks the end of the conversation, which is what caches
// everything before it: the system prompt, the tools and every turn so far.
// Moving the mark to the new tail on each request is the point -- this request
// writes the cache the next one reads.
//
// The marked message is replaced rather than edited. The union holds a pointer
// into the caller's own message, and the agent loop keeps appending to that
// slice: editing in place would leave a stale breakpoint in the middle of the
// next request and change a message the provider has already cached.
//
// Only a string message is marked, which every message this codebase sends is.
// A part array would be a caller we do not have, and a missing breakpoint costs
// money rather than correctness.
func withCacheBreakpoint(messages []openai.ChatCompletionMessageParamUnion) []openai.ChatCompletionMessageParamUnion {
	if len(messages) == 0 {
		return messages
	}

	last := messages[len(messages)-1]
	var marked openai.ChatCompletionMessageParamUnion
	switch {
	case last.OfUser != nil && last.OfUser.Content.OfString.Valid():
		user := *last.OfUser
		user.Content = openai.ChatCompletionUserMessageParamContentUnion{
			OfArrayOfContentParts: []openai.ChatCompletionContentPartUnionParam{
				{OfText: cacheableText(user.Content.OfString.Value)},
			},
		}
		marked = openai.ChatCompletionMessageParamUnion{OfUser: &user}
	case last.OfTool != nil && last.OfTool.Content.OfString.Valid():
		tool := *last.OfTool
		tool.Content = openai.ChatCompletionToolMessageParamContentUnion{
			OfArrayOfContentParts: []openai.ChatCompletionContentPartTextParam{
				*cacheableText(tool.Content.OfString.Value),
			},
		}
		marked = openai.ChatCompletionMessageParamUnion{OfTool: &tool}
	default:
		return messages
	}

	out := make([]openai.ChatCompletionMessageParamUnion, len(messages))
	copy(out, messages)
	out[len(out)-1] = marked
	return out
}

func cacheableText(text string) *openai.ChatCompletionContentPartTextParam {
	part := openai.ChatCompletionContentPartTextParam{Text: text}
	part.SetExtraFields(map[string]any{
		"cache_control": map[string]string{"type": "ephemeral"},
	})
	return &part
}
