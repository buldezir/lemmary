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
		EnvOCRSDK, EnvOCRAPIKey, EnvOCRBaseURL, EnvOCRModel, EnvChatGPTLogin,
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

// A local OCR sidecar is reached by address alone. It has to survive both of
// the checks that a second provider normally faces -- "bring your own key" and
// "name a model" -- because it has neither to give.
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

// The case the feature exists for: no hosted key anywhere, OCR still runs.
// The language model is a separate problem, and the setup wizard still asks.
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

// Managed mode refuses to start on a configuration nobody inside the instance
// could repair. A keyless OCR SDK is not one of those: it needs no key, and its
// address always resolves to the compose default.
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

// AI_CHATGPT_LOGIN opens an SDK that talks to OpenAI's first-party endpoints
// with somebody's personal subscription. A typo read as "off" would leave an
// operator staring at a Settings page with no sign-in button and nothing to
// explain why, so it is strict like AI_MANAGED.
func TestChatGPTLoginIsStrictAndOffByDefault(t *testing.T) {
	clearAIEnv(t)
	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if env.ChatGPTLogin {
		t.Fatal("an unset AI_CHATGPT_LOGIN read as on")
	}

	clearAIEnv(t)
	t.Setenv(EnvChatGPTLogin, "1")
	env, err = AIEnvFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !env.ChatGPTLogin {
		t.Fatal("AI_CHATGPT_LOGIN=1 read as off")
	}

	clearAIEnv(t)
	t.Setenv(EnvChatGPTLogin, "maybe")
	if _, err := AIEnvFromEnv(); err == nil {
		t.Fatal("a misspelled AI_CHATGPT_LOGIN was accepted")
	}
}

// The tenant of a managed instance is not the party whose ChatGPT account would
// be at risk, and the operator already chose and pays for a provider. Refusing
// the combination at parse time beats discovering it from Settings.
func TestChatGPTLoginIsRefusedOnAManagedInstance(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvManaged, "1")
	t.Setenv(EnvChatGPTLogin, "1")
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")
	if _, err := AIEnvFromEnv(); err == nil {
		t.Fatal("AI_MANAGED and AI_CHATGPT_LOGIN were accepted together")
	}
}

// AI_SDK=openai with an opencode.ai base URL was the only way to reach OpenCode
// before the SDK existed, and it is what .env.example shipped. It has to keep
// working, and it has to keep working on a *managed* instance in particular:
// migration 1730000026 moves the provider row, but ApplyManaged re-applies the
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
			// The address the operator gave is still the address used.
			if got := env.Providers.LLM.BaseURL; got != "https://opencode.ai/zen/go/v1" {
				t.Fatalf("base URL = %q", got)
			}
		})
	}
}

// The rule is narrow on purpose: a real OpenAI endpoint, and a URL that merely
// mentions the name in a path, are both left alone.
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

// OCR named as openai on the same OpenCode endpoint is still the same endpoint.
// Read before the two SDKs are compared, or it would look like a second
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

// And the embedding binding is refused on it, which is the honest answer: that
// endpoint has no /embeddings whatever the variable calls it.
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
