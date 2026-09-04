package aiprovider

import "testing"

func TestChatCompletionsURL(t *testing.T) {
	t.Parallel()
	got := ChatCompletionsURL("https://opencode.ai/zen/go/v1/")
	if got != "https://opencode.ai/zen/go/v1/chat/completions" {
		t.Fatalf("ChatCompletionsURL() = %q", got)
	}
	if got := ChatCompletionsURL(""); got != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("default ChatCompletionsURL() = %q", got)
	}
}

func TestEmbeddingsURL(t *testing.T) {
	t.Parallel()
	got := EmbeddingsURL("https://opencode.ai/zen/go/v1/")
	if got != "https://opencode.ai/zen/go/v1/embeddings" {
		t.Fatalf("EmbeddingsURL() = %q", got)
	}
	if got := EmbeddingsURL(""); got != "https://api.openai.com/v1/embeddings" {
		t.Fatalf("default EmbeddingsURL() = %q", got)
	}
}

func TestIsLLM(t *testing.T) {
	t.Parallel()
	if !IsLLM(SDKOpenAI) || !IsLLM(SDKOpenRouter) || !IsLLM(SDKMistral) {
		t.Fatal("openai, openrouter, and mistral should be LLM SDKs")
	}
	if IsLLM(SDKGoogleVision) || IsLLM("unknown") {
		t.Fatal("google_vision and unknown should not be LLM SDKs")
	}
}

func TestDefaultAlias(t *testing.T) {
	t.Parallel()
	if got := DefaultAlias(SDKMistral); got != "Mistral" {
		t.Fatalf("DefaultAlias(mistral) = %q", got)
	}
}

// The local SDK is the one that embeds without chatting, which is why CanEmbed
// exists at all: before it, IsLLM stood in for both questions because every SDK
// that answered one answered the other.
func TestLocalSDKEmbedsWithoutChatting(t *testing.T) {
	t.Parallel()
	if !ValidSDK(SDKLocal) {
		t.Fatal("local must be a valid SDK")
	}
	if IsLLM(SDKLocal) {
		t.Fatal("local cannot serve extraction or chat and must not read as an LLM")
	}
	if !CanEmbed(SDKLocal) {
		t.Fatal("local must be able to serve the embedding binding")
	}
}

// ValidSDK is not the question OCR asks. `local` is a valid SDK and a useless
// OCR provider, and without CanOCR the mismatch would only surface on the first
// document someone uploaded.
func TestCanOCR(t *testing.T) {
	t.Parallel()
	for _, sdk := range []string{SDKOpenAI, SDKOpenRouter, SDKMistral, SDKGoogleVision} {
		if !CanOCR(sdk) {
			t.Fatalf("CanOCR(%q) = false", sdk)
		}
	}
	if CanOCR(SDKLocal) {
		t.Fatal("a local embeddings endpoint cannot read a document")
	}
}

func TestCanEmbed(t *testing.T) {
	t.Parallel()
	for _, sdk := range []string{SDKOpenAI, SDKOpenRouter, SDKMistral, SDKLocal} {
		if !CanEmbed(sdk) {
			t.Fatalf("CanEmbed(%q) = false", sdk)
		}
	}
	// Google Vision reads documents; it has no /embeddings endpoint at all.
	if CanEmbed(SDKGoogleVision) || CanEmbed("unknown") || CanEmbed("") {
		t.Fatal("google_vision, an unknown SDK and an empty SDK cannot embed")
	}
}

// A sidecar on the compose network has nobody to authenticate to, so an empty
// key there is a complete configuration rather than a half-written one.
func TestRequiresAPIKeyAndHasCredential(t *testing.T) {
	t.Parallel()
	if RequiresAPIKey(SDKLocal) {
		t.Fatal("local must not require an API key")
	}
	for _, sdk := range []string{SDKOpenAI, SDKOpenRouter, SDKMistral, SDKGoogleVision} {
		if !RequiresAPIKey(sdk) {
			t.Fatalf("RequiresAPIKey(%q) = false", sdk)
		}
	}

	if !HasCredential(Provider{SDK: SDKLocal}) {
		t.Fatal("a keyless local provider is fully credentialed")
	}
	if HasCredential(Provider{SDK: SDKOpenAI}) {
		t.Fatal("a keyless openai provider is half a configuration")
	}
	if !HasCredential(Provider{SDK: SDKOpenAI, APIKey: "sk-test"}) {
		t.Fatal("a keyed openai provider is credentialed")
	}
	if HasCredential(Provider{SDK: SDKOpenAI, APIKey: "   "}) {
		t.Fatal("whitespace is not a credential")
	}
}

func TestDefaultBaseURLLocalPointsAtTheSidecar(t *testing.T) {
	t.Parallel()
	// The service name in docker-compose.embeddings.yml. If one of these moves
	// the other has to move with it, or the overlay comes up unconfigured.
	if got := DefaultBaseURL(SDKLocal); got != "http://embeddings:80/v1" {
		t.Fatalf("DefaultBaseURL(local) = %q", got)
	}
	if got := DefaultAlias(SDKLocal); got != "Local embeddings" {
		t.Fatalf("DefaultAlias(local) = %q", got)
	}
}
