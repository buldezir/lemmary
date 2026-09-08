package aiprovider

import "testing"

// The chatgpt SDK's whole shape in one place: it chats, it reads documents, it
// cannot embed, and its credential is a token rather than a pasted key. Each of
// those answers gates a different binding, and getting one wrong would put a
// provider somewhere it cannot serve.
func TestChatGPTSDKChatsAndReadsButDoesNotEmbed(t *testing.T) {
	t.Parallel()
	if !IsLLM(SDKChatGPT) {
		t.Error("chatgpt must serve the language-model bindings")
	}
	if CanEmbed(SDKChatGPT) {
		t.Error("the Codex backend serves no /embeddings")
	}
	// Its models take file and image input like any other LLM's, so OCR runs
	// on the seat: see internal/chatgpt.messageContent for the parts, and
	// internal/ocr.NewLLMProvider for the request they go in.
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
	// Every other SDK must keep answering the old way: RequiresOAuth is new,
	// and a stray true would hide a working key behind a sign-in button.
	for _, sdk := range ValidSDKs {
		if sdk != SDKChatGPT && RequiresOAuth(sdk) {
			t.Errorf("%s must not require a sign-in", sdk)
		}
	}
}

// Configured is what the setup wizard, the readiness check and the runtime all
// ask. For chatgpt the answer lives in a different column than for every other
// SDK, and the old spelling would have read a signed-in provider as unusable.
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

// The Codex backend publishes no catalogue, so the picker is served locally --
// for chat and for OCR, which bind the same models. Embeddings stay empty
// rather than offering models for the one binding CanEmbed refuses.
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
