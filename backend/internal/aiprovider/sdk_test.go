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
