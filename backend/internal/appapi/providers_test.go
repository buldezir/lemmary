package appapi

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

func providerBindingsForTest(t *testing.T) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection(config.CollectionName)
	collection.Fields.Add(
		&core.TextField{Name: "ocr_provider_id", Max: 15},
		&core.TextField{Name: "extract_provider_id", Max: 15},
		&core.TextField{Name: "chat_provider_id", Max: 15},
		&core.TextField{Name: "search_provider_id", Max: 15},
		&core.TextField{Name: "search_helper_provider_id", Max: 15},
		&core.TextField{Name: "embedding_provider_id", Max: 15},
	)
	record := core.NewRecord(collection)
	record.Id = config.SingletonID
	return record
}

// The embedding client speaks the OpenAI-shaped /embeddings API, which
// google_vision has no equivalent of. A provider bound only as the embedding
// provider used to slip through the SDK guard, and the failure that followed
// was invisible: Deep Search's dense half simply stopped returning anything.
//
// The guard is CanEmbed rather than IsLLM, which is what lets the local SDK --
// an endpoint that embeds without chatting -- stay bound.
func TestProviderBoundToEmbeddingsMustKeepAnEmbeddingSDK(t *testing.T) {
	t.Parallel()
	record := providerBindingsForTest(t)
	record.Set("embedding_provider_id", "provider1")

	if !boundTo(record, "provider1", embeddingBindingField) {
		t.Fatal("a provider bound as the embedding provider must not be switched to an SDK that cannot embed")
	}
	if boundTo(record, "provider2", embeddingBindingField) {
		t.Fatal("an unbound provider was reported as in use")
	}

	// The binding is not an LLM binding: switching it to the local SDK is
	// exactly the move the SDK exists for, and must not be refused.
	if boundTo(record, "provider1", llmBindingFields...) {
		t.Fatal("the embedding binding must not force an LLM SDK")
	}
	if !aiprovider.CanEmbed(aiprovider.SDKLocal) || aiprovider.IsLLM(aiprovider.SDKLocal) {
		t.Fatal("the local SDK must embed without counting as a language model")
	}
	if aiprovider.CanEmbed(aiprovider.SDKGoogleVision) {
		t.Fatal("google_vision has no /embeddings endpoint and must not pass the guard")
	}
}

func TestBoundToCoversEveryLLMBinding(t *testing.T) {
	t.Parallel()
	for _, field := range llmBindingFields {
		record := providerBindingsForTest(t)
		record.Set(field, "provider1")
		if !boundTo(record, "provider1", llmBindingFields...) {
			t.Fatalf("%s does not guard the SDK switch", field)
		}
	}

	// OCR is the one binding google_vision exists for, so it must not block a
	// switch away from an LLM SDK.
	ocrOnly := providerBindingsForTest(t)
	ocrOnly.Set("ocr_provider_id", "provider1")
	if boundTo(ocrOnly, "provider1", llmBindingFields...) {
		t.Fatal("an OCR-only binding must not force an LLM SDK")
	}
	// It still blocks a delete, though: the id would dangle in settings.
	if !aiprovider.ReferencedBySettings(ocrOnly, "provider1") {
		t.Fatal("an OCR binding has to block a delete")
	}

	if boundTo(nil, "provider1", llmBindingFields...) ||
		boundTo(providerBindingsForTest(t), "  ", llmBindingFields...) {
		t.Fatal("a missing record or a blank id is not a binding")
	}
}

// The OCR binding needed no SDK guard while every SDK but google_vision could
// read a document and google_vision was the one it existed for. The local SDK
// is the first that can be bound here and do nothing at all.
func TestProviderBoundToOCRMustKeepAnOCRSDK(t *testing.T) {
	t.Parallel()
	record := providerBindingsForTest(t)
	record.Set("ocr_provider_id", "provider1")

	if !boundTo(record, "provider1", ocrBindingField) {
		t.Fatal("a provider bound to OCR must not be switched to an SDK that cannot read a document")
	}
	if aiprovider.CanOCR(aiprovider.SDKLocal) {
		t.Fatal("a local embeddings endpoint cannot serve OCR")
	}
	// google_vision must still be allowed here: it is what the binding is for.
	if !aiprovider.CanOCR(aiprovider.SDKGoogleVision) {
		t.Fatal("google_vision is the SDK the OCR binding exists for")
	}
}

// The Deep Search helper does bulk per-document reading on a chat endpoint.
// applySettingsPatch has always refused a non-LLM provider for it on write, but
// the provider patch handler did not, so the same binding could be broken from
// the other side.
func TestSearchHelperBindingGuardsTheSDKSwitch(t *testing.T) {
	t.Parallel()
	record := providerBindingsForTest(t)
	record.Set("search_helper_provider_id", "provider1")
	if !boundTo(record, "provider1", llmBindingFields...) {
		t.Fatal("a bound Deep Search helper provider must stay an LLM SDK")
	}
}
