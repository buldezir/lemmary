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
		&core.TextField{Name: "research_provider_id", Max: 15},
		&core.TextField{Name: "embedding_provider_id", Max: 15},
	)
	record := core.NewRecord(collection)
	record.Id = config.SingletonID
	return record
}

// A provider bound only as the embedding provider could slip through the SDK
// guard, and the failure was invisible: the dense half simply stopped returning
// anything. The guard is CanEmbed rather than IsLLM, so the local SDK, which
// embeds without chatting, stays bound.
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

	// Not an LLM binding: switching it to the local SDK is the move that
	// SDK exists for.
	if boundTo(record, "provider1", llmBindingFields...) {
		t.Fatal("the embedding binding must not force an LLM SDK")
	}
	if !aiprovider.CanEmbed(aiprovider.SDKLocalEmbeddings) || aiprovider.IsLLM(aiprovider.SDKLocalEmbeddings) {
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

	// OCR is the binding google_vision exists for, so it must not block a
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

// The local SDK is the first that can be bound to OCR and do nothing at all.
func TestProviderBoundToOCRMustKeepAnOCRSDK(t *testing.T) {
	t.Parallel()
	record := providerBindingsForTest(t)
	record.Set("ocr_provider_id", "provider1")

	if !boundTo(record, "provider1", ocrBindingField) {
		t.Fatal("a provider bound to OCR must not be switched to an SDK that cannot read a document")
	}
	if aiprovider.CanOCR(aiprovider.SDKLocalEmbeddings) {
		t.Fatal("a local embeddings endpoint cannot serve OCR")
	}
	// google_vision must still be allowed here: it is what the binding is for.
	if !aiprovider.CanOCR(aiprovider.SDKGoogleVision) {
		t.Fatal("google_vision is the SDK the OCR binding exists for")
	}
}

// applySettingsPatch refuses a non-LLM research provider on write; without the
// same guard here the binding could be broken from the other side.
func TestResearchBindingGuardsTheSDKSwitch(t *testing.T) {
	t.Parallel()
	record := providerBindingsForTest(t)
	record.Set("research_provider_id", "provider1")
	if !boundTo(record, "provider1", llmBindingFields...) {
		t.Fatal("a bound Deep Research provider must stay an LLM SDK")
	}
}

// Building the sentence from the list is only a fix if something notices when
// the list grows again.
func TestInvalidSDKMessageNamesEverySDK(t *testing.T) {
	t.Parallel()
	message := invalidSDKMessage()
	for _, sdk := range aiprovider.ValidSDKs {
		if !strings.Contains(message, sdk) {
			t.Errorf("%q does not name %s", message, sdk)
		}
	}
}

// A local OCR provider is not an LLM SDK, so binding it to OCR must not fire
// the LLM guard, but deleting it while OCR points at it still must.
func TestLocalOCRProviderIsBoundButNotToAnLLMFeature(t *testing.T) {
	t.Parallel()
	settings := providerBindingsForTest(t)
	settings.Set("ocr_provider_id", "docling1")

	if boundTo(settings, "docling1", llmBindingFields...) {
		t.Error("an OCR-only binding must not require an LLM SDK")
	}
	if !aiprovider.ReferencedBySettings(settings, "docling1") {
		t.Error("an OCR binding must still block deletion")
	}
}
