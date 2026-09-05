package config

import (
	"strings"
	"testing"

	"lemmary/backend/internal/aiprovider"
)

// One variable is still the ordinary configuration: AI_EMBEDDING_MODEL alone
// embeds on the provider AI_SDK already names, and the embedding provider block
// stays out of the way.
func TestAIEnvReadsTheEmbeddingModel(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")
	t.Setenv(EnvAIEmbeddingModel, "text-embedding-3-small")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.LLM.EmbeddingModel != "text-embedding-3-small" {
		t.Fatalf("embedding model = %q", env.Providers.LLM.EmbeddingModel)
	}
	if got := env.Defaults().EmbeddingModel; got != "text-embedding-3-small" {
		t.Fatalf("Defaults().EmbeddingModel = %q", got)
	}
}

// Unset is the pre-feature behaviour and must stay a working configuration:
// no vectors, keyword search only.
func TestAIEnvWithoutAnEmbeddingModelLeavesTheFeatureOff(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.LLM.EmbeddingModel != "" {
		t.Fatalf("embedding model = %q, want empty", env.Providers.LLM.EmbeddingModel)
	}
	if got := env.Defaults().EmbeddingModel; got != "" {
		t.Fatalf("Defaults().EmbeddingModel = %q, want empty", got)
	}
}

// A managed instance without an embedding model is a perfectly good instance,
// so the validation must not start demanding one.
func TestManagedDoesNotRequireAnEmbeddingModel(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvManaged, "1")
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")

	if _, err := AIEnvFromEnv(); err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
}

// Guessing an embedding endpoint from a chat binding would spend money on every
// document in the archive before failing.
func TestApplyBindingFallbacksNeverInventsAnEmbeddingBinding(t *testing.T) {
	t.Parallel()
	cfg := Config{
		ExtractProviderID: "provider-extract",
		ExtractModel:      "extract-model",
	}

	applyBindingFallbacks(&cfg)

	if cfg.EmbeddingProviderID != "" || cfg.EmbeddingModel != "" {
		t.Fatalf("embedding binding was invented: %q / %q", cfg.EmbeddingProviderID, cfg.EmbeddingModel)
	}
}

func TestHasEmbedding(t *testing.T) {
	t.Parallel()
	provider := &aiprovider.Provider{SDK: aiprovider.SDKOpenAI, APIKey: "sk"}

	cases := map[string]struct {
		cfg  Config
		want bool
	}{
		"a provider with a key and a model": {
			Config{EmbeddingProvider: provider, EmbeddingModel: "text-embedding-3-small"}, true,
		},
		"no provider at all": {
			Config{EmbeddingModel: "text-embedding-3-small"}, false,
		},
		"a provider without a key": {
			Config{EmbeddingProvider: &aiprovider.Provider{SDK: aiprovider.SDKOpenAI}, EmbeddingModel: "m"}, false,
		},
		"a provider without a model": {
			Config{EmbeddingProvider: provider}, false,
		},
		"a whitespace model": {
			Config{EmbeddingProvider: provider, EmbeddingModel: "  "}, false,
		},
		"an SDK that cannot embed": {
			Config{
				EmbeddingProvider: &aiprovider.Provider{SDK: aiprovider.SDKGoogleVision, APIKey: "sk"},
				EmbeddingModel:    "m",
			}, false,
		},
		// The local SDK is the exception to "no key means half a
		// configuration": a sidecar on the compose network has nobody to
		// authenticate to, and reading it as absent would leave dense
		// retrieval silently off on an instance configured for it.
		"a keyless local provider with a model": {
			Config{
				EmbeddingProvider: &aiprovider.Provider{SDK: aiprovider.SDKLocalEmbeddings, BaseURL: "http://embeddings:80/v1"},
				EmbeddingModel:    "BAAI/bge-m3",
			}, true,
		},
		"a keyless local provider without a model": {
			Config{
				EmbeddingProvider: &aiprovider.Provider{SDK: aiprovider.SDKLocalEmbeddings, BaseURL: "http://embeddings:80/v1"},
			}, false,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := HasEmbedding(tc.cfg); got != tc.want {
				t.Fatalf("HasEmbedding() = %v, want %v", got, tc.want)
			}
		})
	}
}

// Unset, the embedding provider block changes nothing: embeddings ride on the
// AI_SDK provider exactly as they did before it existed.
func TestNoEmbeddingSDKLeavesEmbeddingsOnTheLanguageModel(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")
	t.Setenv(EnvAIEmbeddingModel, "text-embedding-3-small")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.Embedding.Requested() {
		t.Fatal("no embedding provider was asked for")
	}
	if !env.Providers.SharesEmbeddingProvider() {
		t.Fatal("embeddings should share the language model's provider")
	}
}

// The case the block exists for: an endpoint on the compose network, with no
// credential because there is nobody to authenticate to.
func TestLocalEmbeddingProviderNeedsNoKey(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")
	t.Setenv(EnvAIEmbeddingSDK, aiprovider.SDKLocalEmbeddings)
	t.Setenv(EnvAIEmbeddingModel, "BAAI/bge-m3")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	spec := env.Providers.Embedding
	if spec.SDK != aiprovider.SDKLocalEmbeddings {
		t.Fatalf("embedding sdk=%q", spec.SDK)
	}
	if spec.APIKey != "" {
		t.Fatalf("embedding key=%q, want none borrowed from the language model", spec.APIKey)
	}
	if spec.BaseURL != aiprovider.DefaultBaseURL(aiprovider.SDKLocalEmbeddings) {
		t.Fatalf("embedding base url=%q", spec.BaseURL)
	}
	// Configured, not "has a key": the sidecar's address is its whole
	// configuration, and NormalizeBaseURL supplied it above.
	if !spec.Configured() {
		t.Fatal("a keyless local spec with an address is a complete configuration")
	}
	if (aiprovider.ProviderSpec{SDK: aiprovider.SDKLocalEmbeddings}).Configured() {
		t.Fatal("a local spec with no address is half a configuration")
	}
	if env.Providers.SharesEmbeddingProvider() {
		t.Fatal("a local endpoint is not the language model's endpoint")
	}
	if !env.Providers.Configured() {
		t.Fatal("a usable embedding provider is part of a configured bootstrap")
	}
}

// Every way to half-write the block, refused with the variable named. Off
// managed mode these still error: the environment seeds once, so a silent
// fallback would bind embeddings somewhere nobody asked for and there would be
// nothing later to correct it.
func TestEmbeddingProviderHalfConfigurations(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"a key without an SDK": {
			env:  map[string]string{EnvAIEmbeddingAPIKey: "sk-other"},
			want: EnvAIEmbeddingSDK,
		},
		"a base URL without an SDK": {
			env:  map[string]string{EnvAIEmbeddingBaseURL: "http://embeddings:80/v1"},
			want: EnvAIEmbeddingSDK,
		},
		"an SDK that cannot embed": {
			env:  map[string]string{EnvAIEmbeddingSDK: aiprovider.SDKGoogleVision, EnvAIEmbeddingModel: "m"},
			want: "cannot serve embeddings",
		},
		"an SDK with no model to bind": {
			env:  map[string]string{EnvAIEmbeddingSDK: aiprovider.SDKLocalEmbeddings},
			want: EnvAIEmbeddingModel,
		},
		"a different SDK that cannot borrow the language model's key": {
			env: map[string]string{
				EnvAIEmbeddingSDK:   aiprovider.SDKMistral,
				EnvAIEmbeddingModel: "mistral-embed",
			},
			want: EnvAIEmbeddingAPIKey,
		},
		// The mirror of the above: `local` is a valid SDK, so ValidSDK alone
		// would let it serve OCR and the mismatch would not surface until
		// somebody uploaded a document.
		"the local SDK asked to do OCR": {
			env: map[string]string{
				EnvOCRSDK:    aiprovider.SDKLocalEmbeddings,
				EnvOCRModel:  "BAAI/bge-m3",
				EnvOCRAPIKey: "unused",
			},
			want: "cannot read a document",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			clearAIEnv(t)
			t.Setenv(EnvAIAPIKey, "sk-test")
			t.Setenv(EnvAIModel, "some-model")
			for key, value := range tc.env {
				t.Setenv(key, value)
			}

			_, err := AIEnvFromEnv()
			if err == nil {
				t.Fatal("expected an error naming the variable")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
}

// Naming the language model's own SDK is a way to say "a different embedding
// model on the same endpoint", so the key and address are reused rather than
// written out twice -- the same courtesy OCR_SDK has always had.
func TestEmbeddingSDKMatchingTheLanguageModelReusesItsEndpoint(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAISDK, aiprovider.SDKOpenAI)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvAIModel, "some-model")
	t.Setenv(EnvAIBaseURL, "https://gateway.example/v1")
	t.Setenv(EnvAIEmbeddingSDK, aiprovider.SDKOpenAI)
	t.Setenv(EnvAIEmbeddingModel, "text-embedding-3-small")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	spec := env.Providers.Embedding
	if spec.APIKey != "sk-test" {
		t.Fatalf("embedding key=%q, want the language model's", spec.APIKey)
	}
	if spec.BaseURL != "https://gateway.example/v1" {
		t.Fatalf("embedding base url=%q, want the language model's", spec.BaseURL)
	}
	if !env.Providers.SharesEmbeddingProvider() {
		t.Fatal("the same SDK is the same endpoint, so no second provider row")
	}
}
