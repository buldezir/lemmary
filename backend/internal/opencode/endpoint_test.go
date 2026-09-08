package opencode

import (
	"testing"

	"lemmary/backend/internal/aiprovider"
)

// Every model id OpenCode Go's docs list, against the endpoint they list it
// under. The prefixes are the implementation; this is the contract they were
// derived from, so a prefix edited into overlapping with another family fails
// here rather than at the first request.
func TestEveryDocumentedModelRoutesToItsEndpoint(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		// OpenAI-compatible /chat/completions
		"glm-5.3-flash":                EndpointChat,
		"glm-5.3":                      EndpointChat,
		"glm-5.2":                      EndpointChat,
		"glm-5.1":                      EndpointChat,
		"kimi-k3":                      EndpointChat,
		"kimi-k2.7-code":               EndpointChat,
		"kimi-k2.6":                    EndpointChat,
		"longcat-2.0":                  EndpointChat,
		"deepseek-v4-pro":              EndpointChat,
		"deepseek-v4-flash":            EndpointChat,
		"deepseek-v4-flash-vision-exp": EndpointChat,
		"mimo-v2.5":                    EndpointChat,
		"mimo-v2.5-pro":                EndpointChat,
		"hy4-preview":                  EndpointChat,
		"hy3":                          EndpointChat,
		"omen-alpha":                   EndpointChat,
		// /responses
		"grok-4.6":                   EndpointResponses,
		"gpt-5.6-luna":               EndpointResponses,
		"muse-spark-1.3-contributor": EndpointResponses,
		"muse-spark-1.2-contributor": EndpointResponses,
		// Anthropic /messages
		"minimax-m3":    EndpointMessages,
		"minimax-m2.7":  EndpointMessages,
		"minimax-m2.5":  EndpointMessages,
		"qwen3.8-max":   EndpointMessages,
		"qwen3.8-flash": EndpointMessages,
		"qwen3.7-max":   EndpointMessages,
		"qwen3.7-plus":  EndpointMessages,
		"qwen3.6-plus":  EndpointMessages,
	}
	for model, want := range cases {
		if got := Endpoint(aiprovider.SDKOpenCode, model); got != want {
			t.Errorf("Endpoint(%q) = %q, want %q", model, got, want)
		}
	}
}

// The default matters as much as the table: OpenCode adds models, and one this
// build has never heard of has to go somewhere. /chat/completions is the
// endpoint whose refusal is legible.
func TestAnUnknownModelGoesToChatCompletions(t *testing.T) {
	t.Parallel()
	for _, model := range []string{"something-new-1", "", "  "} {
		if got := Endpoint(aiprovider.SDKOpenCode, model); got != EndpointChat {
			t.Errorf("Endpoint(%q) = %q, want %q", model, got, EndpointChat)
		}
	}
}

// The routing is OpenCode's, not the model name's. gpt-5.6-luna at
// api.openai.com is served by /chat/completions like anything else there, and
// answering EndpointResponses for it would send an `openai` row down a path its
// own degradation ladder owns.
func TestOnlyTheOpenCodeSDKIsRouted(t *testing.T) {
	t.Parallel()
	for _, sdk := range []string{aiprovider.SDKOpenAI, aiprovider.SDKOpenRouter, aiprovider.SDKMistral, aiprovider.SDKChatGPT, ""} {
		for _, model := range []string{"gpt-5.6-luna", "minimax-m3", "deepseek-v4-flash"} {
			if got := Endpoint(sdk, model); got != "" {
				t.Errorf("Endpoint(%q, %q) = %q, want the empty string", sdk, model, got)
			}
		}
	}
}

// anthropic-sdk-go appends "v1/messages" to the base URL itself, so the /v1
// that every provider row carries has to come off -- left on, the request goes
// to /zen/go/v1/v1/messages and the endpoint 404s.
func TestMessagesBaseURLDropsTheVersionSegment(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"https://opencode.ai/zen/go/v1":  "https://opencode.ai/zen/go/v1/messages",
		"https://opencode.ai/zen/go/v1/": "https://opencode.ai/zen/go/v1/messages",
		// A test server's base URL has no /v1 to strip.
		"http://127.0.0.1:8080": "http://127.0.0.1:8080/v1/messages",
		// Empty falls back to the SDK's documented endpoint rather than
		// building a relative path.
		"": "https://opencode.ai/zen/go/v1/messages",
	}
	for base, want := range cases {
		if got := MessagesURL(base); got != want {
			t.Errorf("MessagesURL(%q) = %q, want %q", base, got, want)
		}
	}
}
