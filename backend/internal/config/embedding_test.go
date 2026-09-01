package config

import (
	"testing"

	"lemmary/backend/internal/aiprovider"
)

// One variable, on the provider AI_SDK already names: naming a second endpoint
// is a Settings-only choice, so the environment cannot half-configure it.
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
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := HasEmbedding(tc.cfg); got != tc.want {
				t.Fatalf("HasEmbedding() = %v, want %v", got, tc.want)
			}
		})
	}
}
