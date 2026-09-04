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
		"NEAR_DUPLICATE_DETECTION_ENABLED",
		"NEAR_DUPLICATE_THRESHOLD", "OCR_TIMEOUT_SEC", "AI_TIMEOUT_SEC",
		"WORKER_TIMEOUT_SEC", "WORKER_MAX_RETRIES", "DEEP_SEARCH_LANGUAGES",
		"EXTRACTION_PROMPT_VERSION",
	} {
		t.Setenv(key, "")
	}
}

// One API key is the whole configuration: it names the language model and, with
// no OCR provider asked for, serves OCR as well.
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

// A dedicated OCR provider is a second endpoint with its own credential.
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
	// Google Vision reads a document without a model, and storing one would
	// fail the Settings page's own validation.
	if got := env.Providers.OCRModel(); got != "" {
		t.Fatalf("ocr model=%q, want empty for google_vision", got)
	}
}

// Naming the same SDK for both means one endpoint, so the key and the model
// carry over rather than having to be written out twice.
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
			// The case that made the managed OCR check unreachable: a named OCR
			// provider with no key used to read as "no OCR asked for", bind OCR
			// to the language model, and bill the LLM for every page.
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

// Off managed mode an absent key is not an error: it is an install that intends
// to be configured from the setup wizard.
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

// AI_MANAGED accepts the same 1/true/yes/on spellings as VAULT_*, and refuses
// anything else rather than reading it as off.
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

// OCR on a provider of its own must not be mistaken for OCR on the language
// model, which is what decides who gets billed for a page.
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
