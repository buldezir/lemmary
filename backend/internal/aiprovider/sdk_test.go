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
	for _, sdk := range []string{SDKGoogleVision, SDKDocling, SDKPaddleOCR, "unknown"} {
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
		{SDKPaddleOCR, false, true, false, true, "http://paddleocr:8080", "PaddleOCR"},
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
	want := map[string]bool{SDKGoogleVision: true, SDKDocling: true, SDKPaddleOCR: true}
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
