package appapi

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
)

func settingsRecordForTest(t *testing.T) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection(config.CollectionName)
	collection.Fields.Add(
		&core.TextField{Name: "ocr_provider_id", Max: 15},
		&core.TextField{Name: "ocr_model", Max: 200},
		&core.TextField{Name: "extract_provider_id", Max: 15},
		&core.TextField{Name: "extract_model", Max: 200},
		&core.TextField{Name: "embedding_provider_id", Max: 15},
		&core.TextField{Name: "embedding_model", Max: 200},
		&core.NumberField{Name: "embedding_dims", OnlyInt: true},
	)
	record := core.NewRecord(collection)
	record.Id = config.SingletonID
	return record
}

func strptr(s string) *string { return &s }

// Every stored vector was produced by one model at one length. Changing either
// makes the recorded length a lie, and a vector index sized from it would
// silently drop everything.
func TestPatchResetsDimensionsWhenTheBindingChanges(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("embedding_model", "text-embedding-3-small")
	record.Set("embedding_dims", 1536)

	err := applySettingsPatch(nil, record, settingsPatchRequest{
		EmbeddingModel: strptr("text-embedding-3-large"),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if got := record.GetInt("embedding_dims"); got != 0 {
		t.Fatalf("embedding_dims = %d, want 0 after a model change", got)
	}
}

func TestPatchKeepsDimensionsWhenTheBindingIsUnchanged(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("embedding_model", "text-embedding-3-small")
	record.Set("embedding_dims", 1536)

	err := applySettingsPatch(nil, record, settingsPatchRequest{
		EmbeddingModel: strptr("text-embedding-3-small"),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if got := record.GetInt("embedding_dims"); got != 1536 {
		t.Fatalf("embedding_dims = %d; re-saving the same model must not reset it", got)
	}
}

// Half a binding reads as "the feature is on" and then fails on every document.
func TestPatchRefusesAnEmbeddingProviderWithoutAModel(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("embedding_provider_id", "provider1")
	record.Set("embedding_model", "text-embedding-3-small")

	err := applySettingsPatch(nil, record, settingsPatchRequest{EmbeddingModel: strptr("  ")})
	if err == nil {
		t.Fatal("clearing the model while a provider is bound should be refused")
	}
}

// Clearing both is how an admin turns dense retrieval off, and it has to be
// allowed.
func TestPatchAllowsClearingTheWholeEmbeddingBinding(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)
	record.Set("embedding_model", "text-embedding-3-small")
	record.Set("embedding_dims", 1536)

	err := applySettingsPatch(nil, record, settingsPatchRequest{
		EmbeddingProviderID: strptr(""),
		EmbeddingModel:      strptr(""),
	})
	if err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if record.GetString("embedding_model") != "" || record.GetInt("embedding_dims") != 0 {
		t.Fatalf("binding was not cleared: %v / %v", record.Get("embedding_model"), record.Get("embedding_dims"))
	}
}

// The operator owns the AI bill in managed mode, and the embedding binding is
// part of it: a tenant able to point it at their own model would be spending
// the operator's key.
func TestTouchesManagedCoversTheEmbeddingBinding(t *testing.T) {
	t.Parallel()

	if !(settingsPatchRequest{EmbeddingProviderID: strptr("provider1")}).touchesManaged() {
		t.Fatal("embedding_provider_id must count as managed")
	}
	if !(settingsPatchRequest{EmbeddingModel: strptr("m")}).touchesManaged() {
		t.Fatal("embedding_model must count as managed")
	}
	// The tenant-owned fields still are not.
	if (settingsPatchRequest{WorkerMaxRetries: new(int)}).touchesManaged() {
		t.Fatal("worker_max_retries is tenant-owned")
	}
}

// embedding_dims has no patch field at all: it is learned from the provider,
// not chosen.
func TestSettingsResponseExposesTheEmbeddingBinding(t *testing.T) {
	t.Parallel()
	got := settingsResponseFromConfig(config.Config{
		EmbeddingProviderID: "provider1",
		EmbeddingModel:      "text-embedding-3-small",
		EmbeddingDims:       1536,
	})

	if got.EmbeddingProviderID != "provider1" || got.EmbeddingModel != "text-embedding-3-small" {
		t.Fatalf("response = %+v", got)
	}
	if got.EmbeddingDims != 1536 {
		t.Fatalf("embedding_dims = %d, want 1536", got.EmbeddingDims)
	}
}
