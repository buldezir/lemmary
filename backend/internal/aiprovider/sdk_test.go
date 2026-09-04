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
	for _, sdk := range []string{SDKGoogleVision, SDKDocling, "unknown"} {
		if IsLLM(sdk) {
			t.Fatalf("%s should not be an LLM SDK", sdk)
		}
	}
}

// TestSDKCapabilities is one table over every SDK, because the three predicates
// have to agree: they are read together at the provider create handler, the
// environment parser and the readiness check, and a row that disagrees with
// itself is how a provider becomes unconfigurable.
func TestSDKCapabilities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		sdk         string
		needsKey    bool
		needsURL    bool
		needsModel  bool
		local       bool
		defaultBase string
		alias       string
	}{
		{SDKOpenAI, true, false, true, false, "https://api.openai.com/v1", "OpenAI"},
		{SDKOpenRouter, true, false, true, false, "https://openrouter.ai/api/v1", "OpenRouter"},
		{SDKMistral, true, false, true, false, "https://api.mistral.ai/v1", "Mistral"},
		{SDKGoogleVision, true, false, false, false, "", "Google Cloud Vision"},
		{SDKDocling, false, true, false, true, "http://docling:5001", "Docling"},
		// The rows that matter most: an unrecognised or empty SDK must fall on
		// the side that still demands a key, because every call site now reads
		// RequiresAPIKey instead of testing the key itself.
		{"tesseract", true, false, true, false, "", "tesseract"},
		{"", true, false, true, false, "", ""},
		{"  ", true, false, true, false, "", "  "},
	}
	for _, tc := range tests {
		t.Run(tc.sdk, func(t *testing.T) {
			if got := RequiresAPIKey(tc.sdk); got != tc.needsKey {
				t.Errorf("RequiresAPIKey(%q) = %v, want %v", tc.sdk, got, tc.needsKey)
			}
			if got := RequiresBaseURL(tc.sdk); got != tc.needsURL {
				t.Errorf("RequiresBaseURL(%q) = %v, want %v", tc.sdk, got, tc.needsURL)
			}
			if got := RequiresOCRModel(tc.sdk); got != tc.needsModel {
				t.Errorf("RequiresOCRModel(%q) = %v, want %v", tc.sdk, got, tc.needsModel)
			}
			if got := IsLocalOCR(tc.sdk); got != tc.local {
				t.Errorf("IsLocalOCR(%q) = %v, want %v", tc.sdk, got, tc.local)
			}
			if got := DefaultBaseURL(tc.sdk); got != tc.defaultBase {
				t.Errorf("DefaultBaseURL(%q) = %q, want %q", tc.sdk, got, tc.defaultBase)
			}
			if got := DefaultAlias(tc.sdk); got != tc.alias {
				t.Errorf("DefaultAlias(%q) = %q, want %q", tc.sdk, got, tc.alias)
			}
		})
	}
}

func TestEverySDKInValidSDKsIsValid(t *testing.T) {
	t.Parallel()
	for _, sdk := range ValidSDKs {
		if !ValidSDK(sdk) {
			t.Errorf("ValidSDK(%q) = false but it is in ValidSDKs", sdk)
		}
	}
	if ValidSDK("tesseract") || ValidSDK("") {
		t.Error("unknown and empty SDKs should not validate")
	}
}

// The environment parser's "needs a model" error enumerates these rather than
// naming google_vision from memory, which is how that sentence went stale.
func TestModellessOCRSDKsMatchesRequiresOCRModel(t *testing.T) {
	t.Parallel()
	want := map[string]bool{SDKGoogleVision: true, SDKDocling: true}
	got := map[string]bool{}
	for _, sdk := range ModellessOCRSDKs() {
		got[sdk] = true
	}
	if len(got) != len(want) {
		t.Fatalf("ModellessOCRSDKs() = %v", ModellessOCRSDKs())
	}
	for sdk := range want {
		if !got[sdk] {
			t.Errorf("%s reads a document without a model but is not listed", sdk)
		}
	}
}

func TestProviderConfigured(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider Provider
		want     bool
	}{
		{"hosted with a key", Provider{SDK: SDKMistral, APIKey: "k"}, true},
		{"hosted without a key", Provider{SDK: SDKMistral, BaseURL: "https://api.mistral.ai/v1"}, false},
		{"local with an address", Provider{SDK: SDKDocling, BaseURL: "http://docling:5001"}, true},
		{"local without an address", Provider{SDK: SDKDocling}, false},
		{"nothing at all", Provider{}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.provider.Configured(); got != tc.want {
				t.Errorf("Configured() = %v, want %v", got, tc.want)
			}
		})
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
// key there is a complete configuration rather than a half-written one. Both
// sidecar SDKs answer this the same way; Provider.Configured is what turns it
// into "reachable", by asking for the address instead.
func TestRequiresAPIKey(t *testing.T) {
	t.Parallel()
	for _, sdk := range []string{SDKLocal, SDKDocling} {
		if RequiresAPIKey(sdk) {
			t.Fatalf("RequiresAPIKey(%q) = true; a sidecar has no account behind it", sdk)
		}
	}
	for _, sdk := range []string{SDKOpenAI, SDKOpenRouter, SDKMistral, SDKGoogleVision} {
		if !RequiresAPIKey(sdk) {
			t.Fatalf("RequiresAPIKey(%q) = false", sdk)
		}
	}

	if !(Provider{SDK: SDKLocal, BaseURL: "http://embeddings:80/v1"}).Configured() {
		t.Fatal("a keyless local provider with an address is fully configured")
	}
	if (Provider{SDK: SDKLocal}).Configured() {
		t.Fatal("a local provider with no address is half a configuration")
	}
	if (Provider{SDK: SDKOpenAI}).Configured() {
		t.Fatal("a keyless openai provider is half a configuration")
	}
	if !(Provider{SDK: SDKOpenAI, APIKey: "sk-test"}).Configured() {
		t.Fatal("a keyed openai provider is configured")
	}
	if (Provider{SDK: SDKOpenAI, APIKey: "   "}).Configured() {
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
