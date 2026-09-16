package config

import (
	"strings"
	"testing"

	"lemmary/backend/internal/aiprovider"
)

// clearAIEnv blanks every variable the parser reads, so a test states its whole
// input and cannot be changed by the developer's own .env.
func clearAIEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		EnvManaged, EnvAISDK, EnvAIAPIKey, EnvAIBaseURL, EnvAIModel, EnvAIEmbeddingModel,
		EnvAIEmbeddingSDK, EnvAIEmbeddingAPIKey, EnvAIEmbeddingBaseURL,
		EnvOCRSDK, EnvOCRAPIKey, EnvOCRBaseURL, EnvOCRModel,
		EnvWebSearchSDK, EnvWebSearchAPIKey, EnvWebSearchBaseURL,
		"NEAR_DUPLICATE_DETECTION_ENABLED",
		"NEAR_DUPLICATE_THRESHOLD", "OCR_TIMEOUT_SEC", "AI_TIMEOUT_SEC",
		"WORKER_TIMEOUT_SEC", "WORKER_MAX_RETRIES", "DEEP_SEARCH_LANGUAGES",
		"EXTRACTION_PROMPT_VERSION", EnvModelCatalogURL,
	} {
		t.Setenv(key, "")
	}
}

func TestOneKeyConfiguresEverything(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.LLM.SDK != aiprovider.SDKOpenAI {
		t.Fatalf("sdk=%q, want the openai default", env.Providers.LLM.SDK)
	}
	if env.Providers.LLM.BaseURL != aiprovider.DefaultBaseURL(aiprovider.SDKOpenAI) {
		t.Fatalf("base url=%q", env.Providers.LLM.BaseURL)
	}
	if !env.Providers.SharesOneProvider() {
		t.Fatal("expected OCR to share the language model's provider")
	}
	if got := env.Providers.OCRModel(); got != "some-model" {
		t.Fatalf("ocr model=%q", got)
	}
	if got := env.Providers.OCRSDK(); got != aiprovider.SDKOpenAI {
		t.Fatalf("ocr sdk=%q", got)
	}
}

func TestSeparateOCRProvider(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")
	t.Setenv(EnvOCRSDK, aiprovider.SDKGoogleVision)
	t.Setenv(EnvOCRAPIKey, "vision-key")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.SharesOneProvider() {
		t.Fatal("expected a provider of its own for OCR")
	}
	// Google Vision reads a document without a model, and storing one would fail
	// the Settings page's own validation.
	if got := env.Providers.OCRModel(); got != "" {
		t.Fatalf("ocr model=%q, want empty for google_vision", got)
	}
}

// A sidecar has neither a key nor a model to give, so it has to survive both
// checks a second provider normally faces.
func TestKeylessOCRSDKNeedsNoKeyAndNoModel(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")
	t.Setenv(EnvOCRSDK, aiprovider.SDKDocling)

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.SharesOneProvider() {
		t.Fatal("expected a provider of its own for OCR")
	}
	if got := env.Providers.OCR.APIKey; got != "" {
		t.Fatalf("ocr api key=%q, want empty", got)
	}
	// The compose service name, so OCR_SDK=docling alone is a complete
	// configuration for anyone running the overlay unedited.
	if got := env.Providers.OCR.BaseURL; got != "http://docling:5001" {
		t.Fatalf("ocr base url=%q", got)
	}
	if got := env.Providers.OCRModel(); got != "" {
		t.Fatalf("ocr model=%q, want empty for docling", got)
	}
	if !env.Providers.OCR.Configured() {
		t.Fatal("a docling spec with an address should be configured")
	}
}

// No hosted key anywhere, and the setup wizard still asks for the LLM.
func TestKeylessOCRConfiguresWithoutAnyHostedKey(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvOCRSDK, aiprovider.SDKDocling)

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.LLM.Configured() {
		t.Fatal("no AI_API_KEY was set; the language model must stay unconfigured")
	}
	if !env.Providers.Configured() {
		t.Fatal("a keyless OCR provider alone should still be a configuration")
	}
	if got := env.Providers.OCRSDK(); got != aiprovider.SDKDocling {
		t.Fatalf("ocr sdk=%q", got)
	}
}

func TestOCRBaseURLOverridesTheSidecarDefault(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvOCRSDK, aiprovider.SDKDocling)
	t.Setenv(EnvOCRBaseURL, "http://ocr.lan:5001/")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if got := env.Providers.OCR.BaseURL; got != "http://ocr.lan:5001" {
		t.Fatalf("ocr base url=%q, want the override, right-trimmed", got)
	}
}

func TestOCRReusesTheLLMCredentialOnTheSameSDK(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAISDK, aiprovider.SDKMistral)
	t.Setenv(EnvAIAPIKey, "sk-mistral")
	t.Setenv(EnvAIModel, "mistral-small-latest")
	t.Setenv(EnvOCRSDK, aiprovider.SDKMistral)
	t.Setenv(EnvOCRModel, "mistral-ocr-latest")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if !env.Providers.SharesOneProvider() {
		t.Fatal("expected one provider for both tasks")
	}
	if env.Providers.OCR.APIKey != "sk-mistral" {
		t.Fatalf("ocr key=%q, want the language model's", env.Providers.OCR.APIKey)
	}
	if env.Providers.OCR.BaseURL != env.Providers.LLM.BaseURL {
		t.Fatalf("ocr base url=%q, want the language model's", env.Providers.OCR.BaseURL)
	}
	if got := env.Providers.OCRModel(); got != "mistral-ocr-latest" {
		t.Fatalf("ocr model=%q", got)
	}
}

func TestParseRejectsMalformedInput(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{
			name: "a non-LLM SDK cannot serve extraction",
			env:  map[string]string{EnvAISDK: aiprovider.SDKGoogleVision},
			want: EnvAISDK,
		},
		{
			name: "an unknown OCR SDK",
			env:  map[string]string{EnvOCRSDK: "tesseract"},
			want: EnvOCRSDK,
		},
		{
			name: "an OCR key with no SDK to use it",
			env:  map[string]string{EnvOCRAPIKey: "orphan"},
			want: EnvOCRSDK,
		},
		{
			name: "an OCR base URL with no SDK to use it",
			env:  map[string]string{EnvOCRBaseURL: "https://ocr.example.test/v1"},
			want: EnvOCRSDK,
		},
		{
			// A named OCR provider with no key used to read as "no OCR asked
			// for", bind OCR to the language model, and bill for every page.
			name: "a named OCR provider with no key of its own",
			env: map[string]string{
				EnvAIAPIKey: "sk-test", EnvOCRSDK: aiprovider.SDKGoogleVision,
			},
			want: EnvOCRAPIKey,
		},
		{
			name: "an unreadable AI_MANAGED, which must never read as off",
			env:  map[string]string{EnvManaged: "ture"},
			want: EnvManaged,
		},
		{
			name: "a second-provider OCR SDK that needs a model",
			env: map[string]string{
				EnvAIAPIKey: "sk-test", EnvOCRSDK: aiprovider.SDKMistral, EnvOCRAPIKey: "sk-mistral",
			},
			want: EnvOCRModel,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAIEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := AIEnvFromEnv()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %s", err, tc.want)
			}
		})
	}
}

// An absent key is an install that intends to be configured from the wizard.
func TestAnEmptyEnvironmentIsFineUntilItIsManaged(t *testing.T) {
	clearAIEnv(t)
	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.Configured() {
		t.Fatal("expected nothing to be configured")
	}

	t.Setenv(EnvManaged, "1")
	_, err = AIEnvFromEnv()
	if err == nil {
		t.Fatal("expected managed mode to refuse an environment with no key")
	}
	if !strings.Contains(err.Error(), EnvAIAPIKey) {
		t.Fatalf("error %q does not name %s", err, EnvAIAPIKey)
	}
}

func TestManagedAcceptsTheDocumentedBooleans(t *testing.T) {
	for _, on := range []string{"1", "true", "yes", "on", "TRUE", " on "} {
		clearAIEnv(t)
		t.Setenv(EnvManaged, on)
		t.Setenv(EnvAIAPIKey, "sk-test")
		t.Setenv(EnvAIModel, "some-model")
		env, err := AIEnvFromEnv()
		if err != nil {
			t.Fatalf("AI_MANAGED=%q: %v", on, err)
		}
		if !env.Managed {
			t.Fatalf("AI_MANAGED=%q read as off", on)
		}
	}
	for _, off := range []string{"", "0", "false", "no", "off"} {
		clearAIEnv(t)
		t.Setenv(EnvManaged, off)
		env, err := AIEnvFromEnv()
		if err != nil {
			t.Fatalf("AI_MANAGED=%q: %v", off, err)
		}
		if env.Managed {
			t.Fatalf("AI_MANAGED=%q read as on", off)
		}
	}
}

// Which provider serves OCR decides who gets billed for a page.
func TestOCRProviderResolution(t *testing.T) {
	cases := []struct {
		name               string
		env                map[string]string
		wantShares         bool
		wantSDK, wantModel string
	}{
		{
			name:       "no OCR named: the language model serves it",
			env:        map[string]string{EnvAIAPIKey: "sk", EnvAIModel: "m"},
			wantShares: true, wantSDK: aiprovider.SDKOpenAI, wantModel: "m",
		},
		{
			name: "the same SDK: one endpoint, a different model",
			env: map[string]string{
				EnvAIAPIKey: "sk", EnvAIModel: "m",
				EnvOCRSDK: aiprovider.SDKOpenAI, EnvOCRModel: "ocr-m",
			},
			wantShares: true, wantSDK: aiprovider.SDKOpenAI, wantModel: "ocr-m",
		},
		{
			name: "its own SDK and key: a second endpoint",
			env: map[string]string{
				EnvAIAPIKey: "sk", EnvAIModel: "m",
				EnvOCRSDK: aiprovider.SDKGoogleVision, EnvOCRAPIKey: "vision",
			},
			wantShares: false, wantSDK: aiprovider.SDKGoogleVision, wantModel: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearAIEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			env, err := AIEnvFromEnv()
			if err != nil {
				t.Fatalf("AIEnvFromEnv: %v", err)
			}
			if got := env.Providers.SharesOneProvider(); got != tc.wantShares {
				t.Errorf("SharesOneProvider()=%v, want %v", got, tc.wantShares)
			}
			if got := env.Providers.OCRSDK(); got != tc.wantSDK {
				t.Errorf("OCRSDK()=%q, want %q", got, tc.wantSDK)
			}
			if got := env.Providers.OCRModel(); got != tc.wantModel {
				t.Errorf("OCRModel()=%q, want %q", got, tc.wantModel)
			}
		})
	}
}

// A keyless OCR SDK needs no key, and its address always resolves to the
// compose default, so managed mode has nothing to refuse.
func TestManagedAcceptsAKeylessOCRSDK(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvManaged, "1")
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")
	t.Setenv(EnvOCRSDK, aiprovider.SDKDocling)

	if _, err := AIEnvFromEnv(); err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
}

func TestManagedAcceptsACompleteEnvironment(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvManaged, "1")
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if !env.Managed {
		t.Fatal("expected managed mode on")
	}
}

// It has to keep working on a managed instance in particular: migration
// 1730000026 moves the provider row, but ApplyManaged re-applies the
// environment on every boot and would move it straight back, with no Settings
// page for anyone inside to intervene.
func TestAnOpenCodeBaseURLIsReadAsTheOpenCodeSDK(t *testing.T) {
	for _, sdk := range []string{aiprovider.SDKOpenAI, aiprovider.SDKOpenRouter} {
		t.Run(sdk, func(t *testing.T) {
			clearAIEnv(t)
			t.Setenv(EnvAISDK, sdk)
			t.Setenv(EnvAIAPIKey, "sk-test")
			t.Setenv(EnvAIBaseURL, "https://opencode.ai/zen/go/v1")

			env, err := AIEnvFromEnv()
			if err != nil {
				t.Fatalf("AIEnvFromEnv: %v", err)
			}
			if got := env.Providers.LLM.SDK; got != aiprovider.SDKOpenCode {
				t.Fatalf("LLM SDK = %q, want %q", got, aiprovider.SDKOpenCode)
			}
			if got := env.Providers.LLM.BaseURL; got != "https://opencode.ai/zen/go/v1" {
				t.Fatalf("base URL = %q", got)
			}
		})
	}
}

// A real OpenAI endpoint, and a URL that merely mentions the name in a path,
// are both left alone.
func TestOtherBaseURLsAreLeftOnTheirSDK(t *testing.T) {
	for name, baseURL := range map[string]string{
		"openai's own":     "https://api.openai.com/v1",
		"a lookalike path": "https://gateway.example.com/opencode.ai/v1",
		"unset":            "",
	} {
		t.Run(name, func(t *testing.T) {
			clearAIEnv(t)
			t.Setenv(EnvAISDK, aiprovider.SDKOpenAI)
			t.Setenv(EnvAIAPIKey, "sk-test")
			if baseURL != "" {
				t.Setenv(EnvAIBaseURL, baseURL)
			}

			env, err := AIEnvFromEnv()
			if err != nil {
				t.Fatalf("AIEnvFromEnv: %v", err)
			}
			if got := env.Providers.LLM.SDK; got != aiprovider.SDKOpenAI {
				t.Fatalf("LLM SDK = %q, want %q", got, aiprovider.SDKOpenAI)
			}
		})
	}
}

// Read before the two SDKs are compared, or this would look like a second
// provider and be refused for having no key of its own.
func TestOCROnTheSameOpenCodeEndpointSharesTheProvider(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAISDK, aiprovider.SDKOpenAI)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIBaseURL, "https://opencode.ai/zen/go/v1")
	t.Setenv(EnvOCRSDK, aiprovider.SDKOpenAI)
	t.Setenv(EnvOCRModel, "deepseek-v4-flash-vision-exp")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if !env.Providers.SharesOneProvider() {
		t.Fatal("OCR was read as a second provider on the same endpoint")
	}
	if got := env.Providers.OCRSDK(); got != aiprovider.SDKOpenCode {
		t.Fatalf("OCR SDK = %q, want %q", got, aiprovider.SDKOpenCode)
	}
	if got := env.Providers.OCR.APIKey; got != "sk-test" {
		t.Fatalf("OCR key = %q, want the language model's", got)
	}
}

func TestEmbeddingsOnAnOpenCodeBaseURLAreRefused(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAISDK, aiprovider.SDKOpenAI)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIBaseURL, "https://opencode.ai/zen/go/v1")
	t.Setenv(EnvAIEmbeddingModel, "text-embedding-3-small")

	if _, err := AIEnvFromEnv(); err == nil {
		t.Fatal("an embedding model on an OpenCode endpoint was accepted")
	}
}

// AI_SDK=anthropic seeds one row that serves extraction, chat, search and OCR,
// with the base URL coming from the SDK. AI_MODEL is not optional here despite
// having a default: that default is gpt-5.6-luna, which is nothing Anthropic
// serves.
func TestTheAnthropicSDKSeedsFromTheEnvironment(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAISDK, aiprovider.SDKAnthropic)
	t.Setenv(EnvAIAPIKey, "sk-ant-test")
	t.Setenv(EnvAIModel, "claude-opus-5")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.LLM.SDK != aiprovider.SDKAnthropic {
		t.Fatalf("sdk = %q, want anthropic", env.Providers.LLM.SDK)
	}
	if env.Providers.LLM.BaseURL != aiprovider.DefaultBaseURL(aiprovider.SDKAnthropic) {
		t.Fatalf("base URL = %q, want the SDK default", env.Providers.LLM.BaseURL)
	}
	// No OCR block, so OCR rides the same provider.
	if env.Providers.OCR.SDK != "" {
		t.Fatalf("OCR sdk = %q, want it to ride the LLM provider", env.Providers.OCR.SDK)
	}
}

// There is no /embeddings on api.anthropic.com at all, so a bound embedding
// model there would fail on the first document rather than at boot.
func TestAnthropicIsRefusedForEmbeddings(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAISDK, aiprovider.SDKAnthropic)
	t.Setenv(EnvAIAPIKey, "sk-ant-test")
	t.Setenv(EnvAIEmbeddingSDK, aiprovider.SDKAnthropic)
	t.Setenv(EnvAIEmbeddingAPIKey, "sk-ant-test")
	t.Setenv(EnvAIEmbeddingModel, "claude-opus-5")

	if _, err := AIEnvFromEnv(); err == nil {
		t.Fatal("anthropic was accepted as an embedding provider")
	}
}
