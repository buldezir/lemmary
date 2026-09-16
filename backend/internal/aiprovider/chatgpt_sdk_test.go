package aiprovider

import "testing"

// The chatgpt SDK's whole shape in one place: it chats, it reads documents, it
// cannot embed, and its credential is a token rather than a pasted key.
func TestChatGPTSDKChatsAndReadsButDoesNotEmbed(t *testing.T) {
	t.Parallel()
	if !IsLLM(SDKChatGPT) {
		t.Error("chatgpt must serve the language-model bindings")
	}
	if CanEmbed(SDKChatGPT) {
		t.Error("the Codex backend serves no /embeddings")
	}
	// Its models take file and image input like any other LLM's, so OCR runs
	// on the seat: see internal/ocr.NewLLMProvider.
	if !CanOCR(SDKChatGPT) {
		t.Error("chatgpt reads documents like the other LLM SDKs")
	}
	if RequiresOCRModel(SDKChatGPT) != true {
		t.Error("an OCR binding on chatgpt names a model, like the other LLM SDKs")
	}
	if !RequiresOAuth(SDKChatGPT) {
		t.Error("chatgpt signs in rather than taking a key")
	}
	if RequiresAPIKey(SDKChatGPT) {
		t.Error("chatgpt has no API key to demand")
	}
	// A stray true elsewhere would hide a working key behind a sign-in button.
	for _, sdk := range ValidSDKs {
		if sdk != SDKChatGPT && RequiresOAuth(sdk) {
			t.Errorf("%s must not require a sign-in", sdk)
		}
	}
}

// Configured is what the setup wizard, the readiness check and the runtime all
// ask. For chatgpt the answer lives in a different column.
func TestChatGPTProviderIsConfiguredByItsToken(t *testing.T) {
	t.Parallel()
	// A base URL is not enough, or the row would read as ready before anyone
	// signed in -- NormalizeBaseURL fills one in for every provider.
	signedOut := Provider{SDK: SDKChatGPT, BaseURL: DefaultBaseURL(SDKChatGPT)}
	if signedOut.Configured() {
		t.Error("a chatgpt provider with no token is not configured")
	}
	signedIn := Provider{SDK: SDKChatGPT, OAuth: `{"access_token":"a"}`}
	if !signedIn.Configured() {
		t.Error("a signed-in chatgpt provider is configured")
	}
	// An API key is not a substitute: it is not what the transport sends.
	keyed := Provider{SDK: SDKChatGPT, APIKey: "sk-test"}
	if keyed.Configured() {
		t.Error("an API key must not stand in for a ChatGPT sign-in")
	}
}

// The Codex backend publishes no catalogue, so the picker is served locally,
// for chat and for OCR. Embeddings stay empty, the binding CanEmbed refuses.
func TestChatGPTModelsAreServedWithoutTheNetwork(t *testing.T) {
	t.Parallel()
	p := Provider{SDK: SDKChatGPT, BaseURL: DefaultBaseURL(SDKChatGPT)}
	for _, purpose := range []ModelPurpose{PurposeLLM, PurposeOCR} {
		models, err := ListModels(t.Context(), p, purpose, nil, nil)
		if err != nil {
			t.Fatalf("%s: listing models for a signed-in provider failed: %v", purpose, err)
		}
		if len(models) == 0 {
			t.Fatalf("%s: the model picker would be empty for every ChatGPT provider", purpose)
		}
	}
	got, err := ListModels(t.Context(), p, PurposeEmbedding, nil, nil)
	if err != nil {
		t.Fatalf("embedding: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("embedding offered %d models for a binding chatgpt cannot serve", len(got))
	}
}
