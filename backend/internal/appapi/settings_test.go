package appapi

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
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
		&core.BoolField{Name: "always_require_review"},
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

// The three binding messages are derived from the capability predicates, so a
// new SDK cannot leave a sentence naming an old list. The OCR one had already
// gone stale once -- it named four SDKs after docling shipped -- which is what
// these pin.
func TestBindingRefusalsNameEverySDKThatCouldServe(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		need    providerNeed
		refused string
		serves  []string
		mustSay []string
	}{
		// refused is a real SDK this binding turns down, which is how the
		// message is reached. CanOCR answers true for anything it does not know,
		// so an invented name would not do -- and would not be reachable anyway,
		// since ValidSDK gates what a row may hold.
		{"llm", needLLM, aiprovider.SDKGoogleVision, aiprovider.LLMSDKs(),
			[]string{aiprovider.SDKChatGPT}},
		{"embedding", needEmbedding, aiprovider.SDKGoogleVision, aiprovider.EmbeddingSDKs(),
			[]string{aiprovider.SDKLocalEmbeddings}},
		{"ocr", needOCR, aiprovider.SDKLocalEmbeddings, aiprovider.OCRSDKs(),
			[]string{aiprovider.SDKDocling, aiprovider.SDKChatGPT}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := providerServes(aiprovider.Provider{SDK: tc.refused}, tc.need)
			if err == nil {
				t.Fatalf("%s was accepted for this binding", tc.refused)
			}
			for _, sdk := range tc.serves {
				if !strings.Contains(err.Error(), sdk) {
					t.Errorf("message omits %s, which can serve this binding: %v", sdk, err)
				}
			}
			// Named explicitly too, so the derivation cannot quietly stop
			// producing the SDKs these messages used to leave out.
			for _, sdk := range tc.mustSay {
				if !strings.Contains(err.Error(), sdk) {
					t.Errorf("message omits %s: %v", sdk, err)
				}
			}
		})
	}

	// And every SDK that can serve is actually accepted, so the lists and the
	// predicates cannot disagree.
	for _, sdk := range aiprovider.OCRSDKs() {
		if err := providerServes(aiprovider.Provider{SDK: sdk}, needOCR); err != nil {
			t.Errorf("OCR refused %s, which OCRSDKs names: %v", sdk, err)
		}
	}
	if err := providerServes(aiprovider.Provider{SDK: aiprovider.SDKLocalEmbeddings}, needOCR); err == nil {
		t.Error("OCR accepted the embeddings-only SDK")
	}
	if err := providerServes(aiprovider.Provider{SDK: aiprovider.SDKChatGPT}, needEmbedding); err == nil {
		t.Error("embeddings accepted chatgpt, whose endpoint has none")
	}
}

func TestPatchTurnsAlwaysRequireReviewOnAndOff(t *testing.T) {
	t.Parallel()
	record := settingsRecordForTest(t)

	if err := applySettingsPatch(nil, record, settingsPatchRequest{
		AlwaysRequireReview: boolptr(true),
	}); err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if !record.GetBool("always_require_review") {
		t.Fatal("always_require_review stayed off after a patch turning it on")
	}

	if err := applySettingsPatch(nil, record, settingsPatchRequest{
		AlwaysRequireReview: boolptr(false),
	}); err != nil {
		t.Fatalf("applySettingsPatch: %v", err)
	}
	if record.GetBool("always_require_review") {
		t.Fatal("always_require_review stayed on after a patch turning it off")
	}
}

// Requiring review buys no API calls, so a managed tenant keeps it. Naming a
// managed field fails the whole PATCH with a 403, which would make the toggle
// unusable on every hosted plan.
func TestAlwaysRequireReviewIsNotAManagedSetting(t *testing.T) {
	t.Parallel()
	if (settingsPatchRequest{AlwaysRequireReview: boolptr(true)}).touchesManaged() {
		t.Fatal("always_require_review counts as managed; a hosted tenant could not set it")
	}
}

func boolptr(b bool) *bool { return &b }

func TestOneOfReadsAsASentence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sdks []string
		want string
	}{
		{nil, "a configured"},
		{[]string{"openai"}, "an openai"},
		{[]string{"openai", "mistral"}, "an openai or mistral"},
		{[]string{"openai", "mistral", "local"}, "an openai, mistral, or local"},
		{[]string{"docling"}, "a docling"},
	}
	for _, tc := range cases {
		if got := oneOf(tc.sdks); got != tc.want {
			t.Errorf("oneOf(%v) = %q, want %q", tc.sdks, got, tc.want)
		}
	}
}

// The branding half of a patch is validated before the record is touched, so a
// value PocketBase would reject cannot leave the rest of the patch applied.
func TestBrandingPatchValidatesNameAndAccent(t *testing.T) {
	t.Parallel()

	name, accent, err := brandingPatch(settingsPatchRequest{
		AppName: strptr("  Archive  "),
		Accent:  strptr("#6E2620"),
	})
	if err != nil {
		t.Fatalf("valid branding rejected: %v", err)
	}
	if name == nil || *name != "Archive" {
		t.Fatalf("app_name = %v, want trimmed Archive", name)
	}
	if accent == nil || *accent != "#6E2620" {
		t.Fatalf("accent = %v, want #6E2620", accent)
	}

	// Absent stays absent: the stored value is kept.
	if name, accent, err = brandingPatch(settingsPatchRequest{}); err != nil || name != nil || accent != nil {
		t.Fatalf("empty patch touched branding: %v %v %v", name, accent, err)
	}

	// Empty clears the accent back to the built-in one.
	if _, accent, err = brandingPatch(settingsPatchRequest{Accent: strptr(" ")}); err != nil || accent == nil || *accent != "" {
		t.Fatalf("blank accent = %v (%v), want cleared", accent, err)
	}

	if _, _, err = brandingPatch(settingsPatchRequest{AppName: strptr("   ")}); err == nil {
		t.Fatal("empty app_name must be refused: PocketBase requires one")
	}
	for _, bad := range []string{"6e2620", "#6e262", "#ggmmbb", "rebeccapurple"} {
		if _, _, err = brandingPatch(settingsPatchRequest{Accent: strptr(bad)}); err == nil {
			t.Fatalf("accent %q must be refused", bad)
		}
	}
}

// Branding is tenant-owned: a managed instance sets the models, not the name.
func TestBrandingIsNotAManagedSetting(t *testing.T) {
	t.Parallel()
	if (settingsPatchRequest{AppName: strptr("Archive"), Accent: strptr("#000000")}).touchesManaged() {
		t.Fatal("app_name and accent are tenant-owned")
	}
}
