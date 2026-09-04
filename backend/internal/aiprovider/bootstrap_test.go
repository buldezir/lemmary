package aiprovider

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// The pure half of Apply's decision: which provider serves OCR.
//
// Worth locking down separately from the parser, because the two disagreed once
// and the disagreement was invisible. A ProviderSpec naming an SDK with no key
// is rejected by config.parseOCR before it can reach Apply, but nothing in this
// package enforces that — so the resolution here must be driven by whether an
// SDK was named, never by whether it happens to carry a credential.
func TestOCRResolutionFollowsTheSDK(t *testing.T) {
	llm := ProviderSpec{SDK: SDKOpenAI, APIKey: "sk", Model: "llm-model"}

	cases := []struct {
		name               string
		ocr                ProviderSpec
		wantShares         bool
		wantSDK, wantModel string
	}{
		{
			name:       "no OCR spec at all",
			ocr:        ProviderSpec{},
			wantShares: true, wantSDK: SDKOpenAI, wantModel: "llm-model",
		},
		{
			name:       "the same SDK, its own model",
			ocr:        ProviderSpec{SDK: SDKOpenAI, APIKey: "sk", Model: "ocr-model"},
			wantShares: true, wantSDK: SDKOpenAI, wantModel: "ocr-model",
		},
		{
			name:       "a different SDK with a key of its own",
			ocr:        ProviderSpec{SDK: SDKGoogleVision, APIKey: "vision"},
			wantShares: false, wantSDK: SDKGoogleVision, wantModel: "",
		},
		{
			// The regression. An SDK named without a key used to read as "no OCR
			// asked for", so OCR bound to the language model and the operator
			// paid the LLM to read every page.
			name:       "a different SDK whose key is missing is still not the language model",
			ocr:        ProviderSpec{SDK: SDKGoogleVision},
			wantShares: false, wantSDK: SDKGoogleVision, wantModel: "",
		},
		{
			// A local sidecar has no key by design, and no model either. The
			// resolution keys on the SDK, so it lands here unchanged.
			name:       "a keyless local SDK",
			ocr:        ProviderSpec{SDK: SDKDocling, BaseURL: "http://docling:5001"},
			wantShares: false, wantSDK: SDKDocling, wantModel: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := Bootstrap{LLM: llm, OCR: tc.ocr}
			if got := b.SharesOneProvider(); got != tc.wantShares {
				t.Errorf("SharesOneProvider()=%v, want %v", got, tc.wantShares)
			}
			if got := b.OCRSDK(); got != tc.wantSDK {
				t.Errorf("OCRSDK()=%q, want %q", got, tc.wantSDK)
			}
			if got := b.OCRModel(); got != tc.wantModel {
				t.Errorf("OCRModel()=%q, want %q", got, tc.wantModel)
			}
		})
	}
}

// Apply must do nothing at all when the environment asked for nothing, so a
// self-hosted install lands on the setup wizard rather than on half a provider.
func TestNothingConfiguredIsNotAnInstruction(t *testing.T) {
	if (Bootstrap{}).Configured() {
		t.Fatal("an empty bootstrap should not count as configured")
	}
	if (Bootstrap{LLM: ProviderSpec{SDK: SDKOpenAI}}).Configured() {
		t.Fatal("an SDK with no key should not count as configured")
	}
	// The keyless SDKs move the requirement rather than removing it: an
	// address instead of a credential. A docling spec with neither is still
	// half a configuration and must not be applied.
	if (Bootstrap{OCR: ProviderSpec{SDK: SDKDocling}}).Configured() {
		t.Fatal("a local SDK with no address should not count as configured")
	}
	if !(Bootstrap{OCR: ProviderSpec{SDK: SDKDocling, BaseURL: "http://docling:5001"}}).Configured() {
		t.Fatal("a local SDK with an address is a complete configuration")
	}
	// The embedding half reads the same way, and only when the SDK was
	// actually asked for: a keyless spec with no SDK named is still nothing.
	if !(Bootstrap{Embedding: ProviderSpec{SDK: SDKLocal, BaseURL: "http://embeddings:80/v1", Model: "BAAI/bge-m3"}}).Configured() {
		t.Fatal("a keyless local embedding provider is a complete instruction")
	}
	if (Bootstrap{Embedding: ProviderSpec{SDK: SDKLocal, Model: "BAAI/bge-m3"}}).Configured() {
		t.Fatal("a local embedding spec with no address is half a configuration")
	}
}

// SharesEmbeddingProvider decides whether Apply writes a second provider row.
// Keying it on whether an SDK was named -- never on whether it has a key -- is
// the same rule SharesOneProvider follows, and for the same reason: conflating
// the two is how a binding silently lands on the language model.
func TestSharesEmbeddingProvider(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		b    Bootstrap
		want bool
	}{
		"nothing asked for": {
			Bootstrap{LLM: ProviderSpec{SDK: SDKOpenAI, APIKey: "sk"}}, true,
		},
		"the same SDK is the same endpoint": {
			Bootstrap{
				LLM:       ProviderSpec{SDK: SDKOpenAI, APIKey: "sk"},
				Embedding: ProviderSpec{SDK: SDKOpenAI, APIKey: "sk", Model: "text-embedding-3-small"},
			}, true,
		},
		"a local endpoint is somewhere else": {
			Bootstrap{
				LLM:       ProviderSpec{SDK: SDKOpenAI, APIKey: "sk"},
				Embedding: ProviderSpec{SDK: SDKLocal, Model: "BAAI/bge-m3"},
			}, false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.b.SharesEmbeddingProvider(); got != tc.want {
				t.Fatalf("SharesEmbeddingProvider() = %v, want %v", got, tc.want)
			}
		})
	}
}

func settingsRecordForTest() *core.Record {
	collection := core.NewBaseCollection("app_settings")
	collection.Fields.Add(
		&core.TextField{Name: "embedding_provider_id", Max: 15},
		&core.TextField{Name: "embedding_model", Max: 200},
		&core.NumberField{Name: "embedding_dims", OnlyInt: true},
	)
	return core.NewRecord(collection)
}

// The dimension count is learned from the provider's first answer, so a boot
// that rewrote it would make every document look stale once a minute. It resets
// only when the binding it describes actually moved.
func TestBindEmbeddingResetsDimsOnlyOnChange(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest()

	bindEmbedding(record, "provider1", "text-embedding-3-small")
	if record.GetString("embedding_provider_id") != "provider1" || record.GetString("embedding_model") != "text-embedding-3-small" {
		t.Fatalf("binding not written: %v / %v", record.Get("embedding_provider_id"), record.Get("embedding_model"))
	}

	record.Set("embedding_dims", 1536)
	bindEmbedding(record, "provider1", "text-embedding-3-small")
	if got := record.GetInt("embedding_dims"); got != 1536 {
		t.Fatalf("re-applying the same binding reset dims to %d", got)
	}

	bindEmbedding(record, "provider1", "text-embedding-3-large")
	if got := record.GetInt("embedding_dims"); got != 0 {
		t.Fatalf("a model change should reset dims, got %d", got)
	}
}

// Removing AI_EMBEDDING_MODEL from a managed instance has to actually turn the
// feature off, not leave the previous binding standing.
func TestBindEmbeddingClearsWhenTheModelIsRemoved(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest()
	bindEmbedding(record, "provider1", "text-embedding-3-small")
	record.Set("embedding_dims", 1536)

	bindEmbedding(record, "provider1", "")

	if record.GetString("embedding_provider_id") != "" || record.GetString("embedding_model") != "" {
		t.Fatalf("binding survived removal: %v / %v", record.Get("embedding_provider_id"), record.Get("embedding_model"))
	}
	if got := record.GetInt("embedding_dims"); got != 0 {
		t.Fatalf("dims = %d, want 0", got)
	}
}

// The embedding provider is a provider like any other: deleting it out from
// under the binding would leave a dangling id in settings.
func TestReferencedBySettingsCoversTheEmbeddingBinding(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest()
	record.Set("embedding_provider_id", "provider1")

	if !ReferencedBySettings(record, "provider1") {
		t.Fatal("a provider bound to embeddings should count as referenced")
	}
}
