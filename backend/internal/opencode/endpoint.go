// Package opencode is the OpenCode Go (Zen) SDK.
//
// OpenCode Go is a gateway in front of a catalogue of third-party models, and it
// does not serve them all the same way: each model lives on one of
// /chat/completions, /responses or /messages, the last of which speaks
// Anthropic's Messages API rather than OpenAI's. So this SDK is a wrapper over
// two others -- openai-go for the first two endpoints, anthropic-sdk-go for the
// third -- with the routing table below deciding which.
//
// The table is static because there is nothing to ask: /v1/models says nothing
// about the shape of the API behind each id.
//
// It is a leaf package: it may use aiprovider, but internal/ai does the routing
// and so imports this, never the other way round.
package opencode

import (
	"strings"

	"lemmary/backend/internal/aiprovider"
)

const (
	// EndpointChat serves most of the catalogue.
	EndpointChat      = "chat_completions"
	EndpointResponses = "responses"
	EndpointMessages  = "messages"
)

// Prefixes rather than the ~28 exact model ids the docs list, so a point
// release routes without a code change. Only the two smaller sets are written
// out; /chat/completions is the default because an unknown model sent there
// gets a normal API error, where /messages would answer a malformed request.
var (
	messagesPrefixes  = []string{"minimax", "qwen"}
	responsesPrefixes = []string{"grok", "gpt-", "muse-spark"}
)

// Endpoint is which of OpenCode Go's three endpoints serves model, and "" for
// every other SDK, so a caller can fall through to the generic path.
func Endpoint(sdk, model string) string {
	if strings.TrimSpace(sdk) != aiprovider.SDKOpenCode {
		return ""
	}
	m := strings.ToLower(strings.TrimSpace(model))
	if hasAnyPrefix(m, messagesPrefixes) {
		return EndpointMessages
	}
	if hasAnyPrefix(m, responsesPrefixes) {
		return EndpointResponses
	}
	return EndpointChat
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}
