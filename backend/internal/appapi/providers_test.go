package appapi

import (
	"strings"
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
func TestProviderBoundToEmbeddingsMustStayAnLLMSDK(t *testing.T) {
	t.Parallel()
	record := providerBindingsForTest(t)
	record.Set("embedding_provider_id", "provider1")

	if !boundToLLMFeature(record, "provider1") {
		t.Fatal("a provider bound as the embedding provider must not be switched to a non-LLM SDK")
	}
	if boundToLLMFeature(record, "provider2") {
		t.Fatal("an unbound provider was reported as in use")
	}
}

func TestBoundToLLMFeatureCoversEveryLLMBinding(t *testing.T) {
	t.Parallel()
	for _, field := range llmBindingFields {
		record := providerBindingsForTest(t)
		record.Set(field, "provider1")
		if !boundToLLMFeature(record, "provider1") {
			t.Fatalf("%s does not guard the SDK switch", field)
		}
	}

	// OCR is the one binding google_vision exists for, so it must not block a
	// switch away from an LLM SDK.
	ocrOnly := providerBindingsForTest(t)
	ocrOnly.Set("ocr_provider_id", "provider1")
	if boundToLLMFeature(ocrOnly, "provider1") {
		t.Fatal("an OCR-only binding must not force an LLM SDK")
	}
	// It still blocks a delete, though: the id would dangle in settings.
	if !aiprovider.ReferencedBySettings(ocrOnly, "provider1") {
		t.Fatal("an OCR binding has to block a delete")
	}

	if boundToLLMFeature(nil, "provider1") || boundToLLMFeature(providerBindingsForTest(t), "  ") {
		t.Fatal("a missing record or a blank id is not a binding")
	}
}

// The sentence naming the accepted SDKs was a literal in two handlers, and both
// still said "openai, openrouter, google_vision, or mistral" while a fifth and
// sixth SDK were being added. Building it from the list is only a fix if
// something notices when the list grows again.
func TestInvalidSDKMessageNamesEverySDK(t *testing.T) {
	t.Parallel()
	message := invalidSDKMessage()
	for _, sdk := range aiprovider.ValidSDKs {
		if !strings.Contains(message, sdk) {
			t.Errorf("%q does not name %s", message, sdk)
		}
	}
}

// A local OCR provider is not an LLM SDK, so binding it to OCR must not make
// the LLM guard fire -- but deleting it while OCR points at it still must.
func TestLocalOCRProviderIsBoundButNotToAnLLMFeature(t *testing.T) {
	t.Parallel()
	settings := providerBindingsForTest(t)
	settings.Set("ocr_provider_id", "docling1")

	if boundToLLMFeature(settings, "docling1") {
		t.Error("an OCR-only binding must not require an LLM SDK")
	}
	if !aiprovider.ReferencedBySettings(settings, "docling1") {
		t.Error("an OCR binding must still block deletion")
	}
}
