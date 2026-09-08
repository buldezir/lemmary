// Package opencode is the OpenCode Go (Zen) SDK.
//
// OpenCode Go is a gateway in front of a catalogue of third-party models, and it
// does not serve them all the same way: each model lives on one of
// /chat/completions, /responses or /messages, the last of which speaks
// Anthropic's Messages API rather than OpenAI's. So this SDK is a wrapper over
// two others -- openai-go for the first two endpoints, anthropic-sdk-go for the
// third -- with the routing table below deciding which.
//
// The table is static because there is nothing to ask: /v1/models answers with
// {id, object, created, owned_by} and says nothing about the shape of the API
// behind each id. Endpoint routing used to be inferred from the shape of a
// failed request instead, which cost one rejected request per model per process
// and could not reach /messages at all.
//
// It is a leaf package: it may use aiprovider, but internal/ai does the routing
// and so imports this, never the other way round.
package opencode

import (
	"strings"

	"lemmary/backend/internal/aiprovider"
)

const (
	// EndpointChat is the OpenAI-compatible /chat/completions endpoint, which
	// serves most of the catalogue.
	EndpointChat = "chat_completions"
	// EndpointResponses is OpenAI's /responses endpoint.
	EndpointResponses = "responses"
	// EndpointMessages is Anthropic's /messages endpoint.
	EndpointMessages = "messages"
)

// Prefixes rather than the ~28 exact model ids the docs list, so a point
// release routes without a code change: the endpoint is a property of the model
// family, and OpenCode versions within a family (glm-5.1 through glm-5.3,
// qwen3.6 through qwen3.8) without moving it.
//
// Only the two smaller sets are written out. /chat/completions is the default
// because it is the majority and because it is the endpoint whose failure is
// legible -- an unknown model sent there gets a normal API error naming the
// problem, where /messages would answer a malformed request.
var (
	messagesPrefixes  = []string{"minimax", "qwen"}
	responsesPrefixes = []string{"grok", "gpt-", "muse-spark"}
)

// Endpoint is which of OpenCode Go's three endpoints serves model.
//
// It answers "" for every other SDK, so a caller can switch on it and fall
// through to the generic OpenAI-compatible path without asking about the SDK
// twice.
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
