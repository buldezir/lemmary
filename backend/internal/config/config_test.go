package config

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

// The stored fields stay as written, so Settings can show an empty research
// binding; the fallback lives in the accessor the research loop reads.
func TestResearchBindingFallsBackToGeneral(t *testing.T) {
	general := Config{ExtractProviderID: "provider-extract", ExtractModel: "extract-model"}
	if id, model := general.ResearchBinding(); id != "provider-extract" || model != "extract-model" {
		t.Fatalf("research should fall back to the general model, got %q/%q", id, model)
	}
	if general.ResearchProviderID != "" || general.ResearchModel != "" {
		t.Fatal("the stored research binding must stay empty")
	}

	explicit := general
	explicit.ResearchProviderID, explicit.ResearchModel = "provider-research", "research-model"
	if id, model := explicit.ResearchBinding(); id != "provider-research" || model != "research-model" {
		t.Fatalf("explicit research binding was not used: %q/%q", id, model)
	}

	// Half a binding is no binding: a provider with no model cannot be called.
	half := general
	half.ResearchProviderID = "provider-research"
	if id, model := half.ResearchBinding(); id != "provider-extract" || model != "extract-model" {
		t.Fatalf("a half-set research binding should fall back, got %q/%q", id, model)
	}
}

func TestNormalizeLanguageList(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"de, EN , uk", "de,en,uk"},
		{"de,de,DE", "de"},
		{" , , ", ""},
		{"uk", "uk"},
	}
	for _, tc := range cases {
		if got := NormalizeLanguageList(tc.in); got != tc.want {
			t.Fatalf("NormalizeLanguageList(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHasLLM(t *testing.T) {
	openAI := &aiprovider.Provider{SDK: aiprovider.SDKOpenAI, APIKey: "key"}
	noKey := &aiprovider.Provider{SDK: aiprovider.SDKOpenAI}
	vision := &aiprovider.Provider{SDK: aiprovider.SDKGoogleVision, APIKey: "key"}

	if HasLLM(Config{}) {
		t.Fatal("expected no LLM without any provider")
	}
	if HasLLM(Config{ExtractProvider: noKey}) {
		t.Fatal("expected no LLM without an API key")
	}
	if HasLLM(Config{ExtractProvider: vision}) {
		t.Fatal("expected google_vision not to count as an LLM")
	}
	if !HasLLM(Config{ExtractProvider: openAI}) {
		t.Fatal("expected an OpenAI extract provider to count")
	}
}

func TestHasOCR(t *testing.T) {
	vision := &aiprovider.Provider{SDK: aiprovider.SDKGoogleVision, APIKey: "key"}
	mistral := &aiprovider.Provider{SDK: aiprovider.SDKMistral, APIKey: "key"}

	if HasOCR(Config{}) {
		t.Fatal("expected no OCR without a provider")
	}
	if HasOCR(Config{OCRProvider: &aiprovider.Provider{SDK: aiprovider.SDKGoogleVision}}) {
		t.Fatal("expected no OCR without an API key")
	}
	if !HasOCR(Config{OCRProvider: vision}) {
		t.Fatal("expected google_vision to need no model")
	}
	if HasOCR(Config{OCRProvider: mistral}) {
		t.Fatal("expected mistral OCR to require a model")
	}
	if !HasOCR(Config{OCRProvider: mistral, OCRModel: "mistral-ocr-latest"}) {
		t.Fatal("expected mistral OCR with a model to be usable")
	}

	// A local sidecar carries an address where a hosted provider carries a
	// credential; asking for the key here reports a working container as
	// unconfigured with the setup wizard in front of it.
	docling := &aiprovider.Provider{SDK: aiprovider.SDKDocling, BaseURL: "http://docling:5001"}
	if !HasOCR(Config{OCRProvider: docling}) {
		t.Fatal("expected docling to need neither a key nor a model")
	}
	if HasOCR(Config{OCRProvider: &aiprovider.Provider{SDK: aiprovider.SDKDocling}}) {
		t.Fatal("expected no OCR for a local provider with no address")
	}
}

func TestDefaultsUsesCodeDefaults(t *testing.T) {
	for _, key := range []string{
		"AI_TIMEOUT_SEC", "OCR_TIMEOUT_SEC", "WORKER_TIMEOUT_SEC", "WORKER_MAX_RETRIES",
		"AI_SDK", "AI_API_KEY", "AI_MODEL", "AI_BASE_URL", "OCR_SDK", "OCR_API_KEY", "OCR_MODEL",
		"DEEP_SEARCH_LANGUAGES", "EXTRACTION_PROMPT_VERSION",
		"NEAR_DUPLICATE_DETECTION_ENABLED", "NEAR_DUPLICATE_THRESHOLD", "WORKER_CRON_EXPR",
		"AI_MANAGED",
	} {
		t.Setenv(key, "")
	}

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	cfg := env.Defaults()

	if cfg.OCRTimeout != 40*time.Second {
		t.Fatalf("ocr timeout=%s", cfg.OCRTimeout)
	}
	if cfg.OpenAITimeout != 60*time.Second {
		t.Fatalf("ai timeout=%s", cfg.OpenAITimeout)
	}
	if cfg.WorkerTimeout != 300*time.Second {
		t.Fatalf("worker timeout=%s", cfg.WorkerTimeout)
	}
	if cfg.NearDuplicateThreshold != DefaultNearDuplicateThreshold {
		t.Fatalf("threshold=%v", cfg.NearDuplicateThreshold)
	}
	if cfg.NearDuplicateDetectionEnabled {
		t.Fatal("expected near-duplicate detection off by default")
	}
	// Off is the pre-Inbox behaviour, so upgrading changes nothing until asked.
	if cfg.AlwaysRequireReview {
		t.Fatal("expected always-require-review off by default")
	}
	if cfg.WorkerCronExpr != "* * * * *" {
		t.Fatalf("cron=%q", cfg.WorkerCronExpr)
	}
	if env.Managed {
		t.Fatal("expected managed mode off by default")
	}
}

// A zero interval would make the consume folder scan every tick, so the seed
// writes the default rather than the zero value.
func TestApplyConfigToRecordDefaultsIngestInterval(t *testing.T) {
	collection := core.NewBaseCollection(CollectionName)
	collection.Fields.Add(
		&core.NumberField{Name: "ingest_dir_interval_min", OnlyInt: true},
		&core.BoolField{Name: "ingest_dir_delete_original"},
	)
	record := core.NewRecord(collection)
	applyConfigToRecord(record, Config{})
	if got := record.GetInt("ingest_dir_interval_min"); got != DefaultIngestDirIntervalMin {
		t.Fatalf("ingest_dir_interval_min = %d, want %d", got, DefaultIngestDirIntervalMin)
	}
}

func TestDefaultsShareOneModel(t *testing.T) {
	t.Setenv("AI_API_KEY", "key")
	t.Setenv("AI_MODEL", "base-model")
	t.Setenv("OCR_SDK", "")
	t.Setenv("OCR_API_KEY", "")
	t.Setenv("OCR_MODEL", "")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	cfg := env.Defaults()

	if cfg.ExtractModel != "base-model" || cfg.ResearchModel != "" {
		t.Fatalf("models=%q/%q", cfg.ExtractModel, cfg.ResearchModel)
	}
	if cfg.OCRModel != "base-model" {
		t.Fatalf("ocr model=%q", cfg.OCRModel)
	}
}

func TestGetEnvBool(t *testing.T) {
	t.Setenv("LEMMARY_TEST_BOOL", "")
	if got := getEnvBool("LEMMARY_TEST_BOOL", true); !got {
		t.Fatal("expected fallback for an unset value")
	}
	t.Setenv("LEMMARY_TEST_BOOL", "not-a-bool")
	if got := getEnvBool("LEMMARY_TEST_BOOL", true); !got {
		t.Fatal("expected fallback for an unparsable value")
	}
	t.Setenv("LEMMARY_TEST_BOOL", "true")
	if got := getEnvBool("LEMMARY_TEST_BOOL", false); !got {
		t.Fatal("expected true")
	}
}

func TestGetEnvFloatRejectsOutOfRange(t *testing.T) {
	cases := []string{"0", "-1", "1.5", "abc", ""}
	for _, raw := range cases {
		t.Setenv("LEMMARY_TEST_FLOAT", raw)
		if got := getEnvFloat("LEMMARY_TEST_FLOAT", 0.9); got != 0.9 {
			t.Fatalf("getEnvFloat(%q)=%v, want fallback", raw, got)
		}
	}
	t.Setenv("LEMMARY_TEST_FLOAT", "0.75")
	if got := getEnvFloat("LEMMARY_TEST_FLOAT", 0.9); got != 0.75 {
		t.Fatalf("getEnvFloat=%v", got)
	}
}
