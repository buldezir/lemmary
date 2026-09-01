package config

import (
	"testing"
	"time"

	"lemmary/backend/internal/aiprovider"
)

func TestApplyBindingFallbacksChainsExtractToChatToSearch(t *testing.T) {
	cfg := Config{
		ExtractProviderID: "provider-extract",
		ExtractModel:      "extract-model",
	}

	applyBindingFallbacks(&cfg)

	if cfg.ChatProviderID != "provider-extract" || cfg.ChatModel != "extract-model" {
		t.Fatalf("chat should fall back to extract, got %q/%q", cfg.ChatProviderID, cfg.ChatModel)
	}
	if cfg.SearchProviderID != "provider-extract" || cfg.SearchModel != "extract-model" {
		t.Fatalf("search should fall back through chat, got %q/%q", cfg.SearchProviderID, cfg.SearchModel)
	}
}

func TestApplyBindingFallbacksKeepsExplicitValues(t *testing.T) {
	cfg := Config{
		ExtractProviderID: "provider-extract",
		ExtractModel:      "extract-model",
		ChatProviderID:    "provider-chat",
		ChatModel:         "chat-model",
		SearchProviderID:  "provider-search",
		SearchModel:       "search-model",
	}

	applyBindingFallbacks(&cfg)

	if cfg.ChatProviderID != "provider-chat" || cfg.ChatModel != "chat-model" {
		t.Fatalf("explicit chat binding was overwritten: %q/%q", cfg.ChatProviderID, cfg.ChatModel)
	}
	if cfg.SearchProviderID != "provider-search" || cfg.SearchModel != "search-model" {
		t.Fatalf("explicit search binding was overwritten: %q/%q", cfg.SearchProviderID, cfg.SearchModel)
	}
}

// Search inherits from chat, not straight from extract, when only chat is set.
func TestApplyBindingFallbacksSearchPrefersChat(t *testing.T) {
	cfg := Config{
		ExtractProviderID: "provider-extract",
		ExtractModel:      "extract-model",
		ChatProviderID:    "provider-chat",
		ChatModel:         "chat-model",
	}

	applyBindingFallbacks(&cfg)

	if cfg.SearchProviderID != "provider-chat" || cfg.SearchModel != "chat-model" {
		t.Fatalf("search should inherit chat, got %q/%q", cfg.SearchProviderID, cfg.SearchModel)
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
	// Falls back to the chat binding when extraction has none.
	if !HasLLM(Config{ChatProvider: openAI}) {
		t.Fatal("expected the chat provider to be used as a fallback")
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
	if cfg.WorkerCronExpr != "* * * * *" {
		t.Fatalf("cron=%q", cfg.WorkerCronExpr)
	}
	if env.Managed {
		t.Fatal("expected managed mode off by default")
	}
}

// One model serves extraction, chat and Deep Search: the three separate
// variables that used to pin them are gone, and so is the fallback chain
// between them.
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

	if cfg.ExtractModel != "base-model" || cfg.ChatModel != "base-model" || cfg.SearchModel != "base-model" {
		t.Fatalf("models=%q/%q/%q", cfg.ExtractModel, cfg.ChatModel, cfg.SearchModel)
	}
	// OCR with no provider of its own runs on the language model.
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

// Out-of-range thresholds fall back rather than disabling detection silently.
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
