package aiprovider

import (
	"strings"

	"github.com/openai/openai-go/option"
)

// UserAgent names us on every provider call we make ourselves, so a provider
// reading its logs sees which app is calling. No version: nothing in the build
// stamps one.
const UserAgent = "Lemmary"

// UserAgentOptions is the SDK option that stamps UserAgent, and nothing at all
// for SDKChatGPT: the Codex backend checks the originator header against a
// whitelist, so that one path keeps looking exactly like the SDK it
// impersonates.
func UserAgentOptions(sdk string) []option.RequestOption {
	if strings.TrimSpace(sdk) == SDKChatGPT {
		return nil
	}
	return []option.RequestOption{option.WithHeader("User-Agent", UserAgent)}
}
