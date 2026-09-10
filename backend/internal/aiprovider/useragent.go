package aiprovider

import (
	"strings"

	"github.com/openai/openai-go/option"
)

// UserAgent names us on every provider call we make ourselves, in place of the
// SDK's "OpenAI/Go x.y.z" or net/http's "Go-http-client/1.1". A provider
// looking at its logs sees which app is calling, and a gateway that rate-limits
// or blocks by agent has something of ours to name.
//
// No version: nothing in the build stamps one, and an agent claiming a version
// that never moves is worse than one that claims none.
const UserAgent = "Lemmary"

// UserAgentOptions is the SDK option that stamps UserAgent, and nothing at all
// for SDKChatGPT.
//
// The Codex backend serves only its own clients: it checks the originator
// header against a whitelist and refuses anything else with a 403 (see
// internal/chatgpt). An agent it has never seen is the next thing it would
// notice, so that one path keeps looking exactly like the SDK it impersonates.
func UserAgentOptions(sdk string) []option.RequestOption {
	if strings.TrimSpace(sdk) == SDKChatGPT {
		return nil
	}
	return []option.RequestOption{option.WithHeader("User-Agent", UserAgent)}
}
