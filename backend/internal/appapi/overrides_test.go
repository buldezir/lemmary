package appapi

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chat"
	"lemmary/backend/internal/config"
)

// sessionForTest carries a stored binding without a database behind it;
// conversationBinding only reads two fields.
func sessionForTest(providerID, model string) *core.Record {
	collection := core.NewBaseCollection(chat.SessionsCollection)
	collection.Fields.Add(
		&core.TextField{Name: "provider", Max: 15},
		&core.TextField{Name: "model", Max: 200},
	)
	record := core.NewRecord(collection)
	record.Set("provider", providerID)
	record.Set("model", model)
	return record
}

// Ignored rather than refused, unlike a mode mismatch: the client is echoing
// the binding it loaded, not choosing one.
func TestConversationBinding(t *testing.T) {
	t.Parallel()
	requested := aiprovider.Binding{ProviderID: "requested", Model: "gpt-6-astra"}

	cases := map[string]struct {
		session *core.Record
		want    aiprovider.Binding
	}{
		"a new conversation takes the request's binding": {
			session: nil,
			want:    requested,
		},
		"an existing conversation keeps its own": {
			session: sessionForTest("stored", "gpt-5.6-sol"),
			want:    aiprovider.Binding{ProviderID: "stored", Model: "gpt-5.6-sol"},
		},
		// A chat opened on the configured model stays on it, even when a
		// stale picker sends something else.
		"an existing conversation with no binding stays unbound": {
			session: sessionForTest("", ""),
			want:    aiprovider.Binding{},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := conversationBinding(tc.session, requested); got != tc.want {
				t.Fatalf("conversationBinding() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestConversationBindingNormalizesTheRequest(t *testing.T) {
	t.Parallel()
	got := conversationBinding(nil, aiprovider.Binding{ProviderID: " p1 ", Model: " gpt-6-astra "})
	if got != (aiprovider.Binding{ProviderID: "p1", Model: "gpt-6-astra"}) {
		t.Fatalf("conversationBinding() = %+v", got)
	}
}

// Each purpose reads a different pair of fields; a missing case would name the
// OCR model in a chat.
func TestPreferredBinding(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		OCRProviderID:       "ocr",
		OCRModel:            "ocr-model",
		ExtractProviderID:   "extract",
		ExtractModel:        "extract-model",
		ChatProviderID:      "chat",
		ChatModel:           "chat-model",
		SearchProviderID:    "search",
		SearchModel:         "search-model",
		EmbeddingProviderID: "embedding",
		EmbeddingModel:      "embedding-model",
	}
	cases := []struct {
		purpose aiprovider.ModelPurpose
		name    string
		want    [2]string
	}{
		{aiprovider.PurposeOCR, "", [2]string{"ocr", "ocr-model"}},
		{aiprovider.PurposeEmbedding, "", [2]string{"embedding", "embedding-model"}},
		// Unnamed, the LLM purpose answers with chat: it is the only LLM picker
		// a non-admin reaches without naming one.
		{aiprovider.PurposeLLM, "", [2]string{"chat", "chat-model"}},
		// Named, because the capability cannot tell three language-model
		// bindings apart.
		{aiprovider.PurposeLLM, "search", [2]string{"search", "search-model"}},
		{aiprovider.PurposeLLM, "extract", [2]string{"extract", "extract-model"}},
		{aiprovider.PurposeLLM, "chat", [2]string{"chat", "chat-model"}},
		// An unrecognised name falls back to the purpose rather than answering
		// with nothing.
		{aiprovider.PurposeLLM, "sideways", [2]string{"chat", "chat-model"}},
	}
	for _, tc := range cases {
		id, model := preferredBinding(cfg, tc.purpose, tc.name)
		if id != tc.want[0] || model != tc.want[1] {
			t.Fatalf("preferredBinding(%q, %q) = %q/%q, want %q/%q",
				tc.purpose, tc.name, id, model, tc.want[0], tc.want[1])
		}
	}
}

// A conversation records the model it runs on, so changing Settings later
// cannot move it mid-transcript.
func TestRecordedBinding(t *testing.T) {
	t.Parallel()
	override := aiprovider.Binding{ProviderID: "chosen", Model: "chosen-model"}

	cases := map[string]struct {
		requested aiprovider.Binding
		provider  string
		model     string
		want      aiprovider.Binding
	}{
		"an override is recorded as chosen": {
			requested: override,
			provider:  "configured",
			model:     "configured-model",
			want:      override,
		},
		"no override records the configured pair": {
			provider: "configured",
			model:    "configured-model",
			want:     aiprovider.Binding{ProviderID: "configured", Model: "configured-model"},
		},
		// Half a pair is refused by aiprovider.Resolve, so recording one would
		// turn a working chat into a bad request. Nothing recorded means
		// "follow Settings".
		"a provider with no configured model records nothing": {
			provider: "configured",
			want:     aiprovider.Binding{},
		},
		"nothing configured records nothing": {
			want: aiprovider.Binding{},
		},
		// Passed through rather than read as "no override", which Binding.Empty
		// would: substituting the configured model would run one the request
		// did not ask for. Resolve refuses this pair.
		"a model with no provider is not an absent override": {
			requested: aiprovider.Binding{Model: "gpt-6-astra"},
			provider:  "configured",
			model:     "configured-model",
			want:      aiprovider.Binding{Model: "gpt-6-astra"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := recordedBinding(tc.requested, tc.provider, tc.model); got != tc.want {
				t.Fatalf("recordedBinding() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
