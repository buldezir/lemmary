package config

import (
	"strings"
	"testing"

	"lemmary/backend/internal/aiprovider"
)

// Unset is the pre-flag behaviour: nothing reaches the web, and that is a
// working instance rather than a broken one.
func TestAIEnvWithoutAWebSearchProviderLeavesTheToolsOff(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	if env.Providers.WebSearch.Requested() {
		t.Fatalf("web search spec = %+v, want none", env.Providers.WebSearch)
	}
}

func TestAIEnvSeedsTheWebSearchProvider(t *testing.T) {
	clearAIEnv(t)
	t.Setenv(EnvAIAPIKey, "sk-test")
	t.Setenv(EnvWebSearchSDK, aiprovider.SDKTavily)
	t.Setenv(EnvWebSearchAPIKey, "tvly-test")

	env, err := AIEnvFromEnv()
	if err != nil {
		t.Fatalf("AIEnvFromEnv: %v", err)
	}
	spec := env.Providers.WebSearch
	if spec.SDK != aiprovider.SDKTavily || spec.APIKey != "tvly-test" {
		t.Fatalf("web search spec = %+v", spec)
	}
	// The endpoint is filled in, so naming the SDK and a key is the whole
	// configuration.
	if spec.BaseURL != "https://api.tavily.com" {
		t.Fatalf("base URL = %q, want Tavily's documented endpoint", spec.BaseURL)
	}
	if !spec.Configured() {
		t.Fatal("an SDK with a key is a complete configuration")
	}
}

func TestAIEnvRefusesAHalfWrittenWebSearchBlock(t *testing.T) {
	tests := []struct {
		name    string
		sdk     string
		key     string
		baseURL string
		wantSub string
	}{
		// There is nothing to fold a stray key into: no other provider searches
		// the web, so guessing would configure something nobody asked for.
		{"key without an SDK", "", "tvly-test", "", EnvWebSearchSDK},
		{"base URL without an SDK", "", "", "https://proxy.example", EnvWebSearchSDK},
		{"an SDK that cannot search", aiprovider.SDKOpenAI, "sk-test", "", "cannot search the web"},
		{"an SDK with no key", aiprovider.SDKTavily, "", "", EnvWebSearchAPIKey},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clearAIEnv(t)
			t.Setenv(EnvAIAPIKey, "sk-test")
			t.Setenv(EnvWebSearchSDK, tc.sdk)
			t.Setenv(EnvWebSearchAPIKey, tc.key)
			t.Setenv(EnvWebSearchBaseURL, tc.baseURL)

			_, err := AIEnvFromEnv()
			if err == nil {
				t.Fatal("AIEnvFromEnv should refuse this")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want it to mention %q", err, tc.wantSub)
			}
		})
	}
}

// HasWebSearch is what decides whether the tools can be offered at all, and it
// has no model term: a web-search API takes none.
func TestHasWebSearch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider *aiprovider.Provider
		want     bool
	}{
		{"bound and keyed", &aiprovider.Provider{SDK: aiprovider.SDKTavily, APIKey: "k"}, true},
		{"bound without a key", &aiprovider.Provider{SDK: aiprovider.SDKTavily}, false},
		// The guard that matters: an LLM row bound here cannot search the web,
		// and reading it as available would offer a tool that always fails.
		{"an SDK that cannot search", &aiprovider.Provider{SDK: aiprovider.SDKOpenAI, APIKey: "k"}, false},
		{"nothing bound", nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasWebSearch(Config{WebSearchProvider: tc.provider}); got != tc.want {
				t.Errorf("HasWebSearch() = %v, want %v", got, tc.want)
			}
		})
	}
}

// buildWebSearch is what /meta's web_search ultimately reports, so the same
// refusals have to hold there.
func TestBuildWebSearchOnlyBuildsAClientForAUsableProvider(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider *aiprovider.Provider
		wantNil  bool
	}{
		{"bound and keyed", &aiprovider.Provider{SDK: aiprovider.SDKTavily, APIKey: "k"}, false},
		{"bound without a key", &aiprovider.Provider{SDK: aiprovider.SDKTavily}, true},
		{"an SDK that cannot search", &aiprovider.Provider{SDK: aiprovider.SDKMistral, APIKey: "k"}, true},
		{"nothing bound", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := buildWebSearch(nil, Config{}, tc.provider, nil)
			if (client == nil) != tc.wantNil {
				t.Errorf("buildWebSearch() nil = %v, want %v", client == nil, tc.wantNil)
			}
		})
	}
}

// A fresh runtime has no snapshot yet, and /meta must read that as off rather
// than panicking or offering a toggle that cannot work.
func TestWebSearchAvailableIsOffBeforeAnythingIsBound(t *testing.T) {
	t.Parallel()
	if NewRuntime(AIEnv{}).WebSearchAvailable() {
		t.Fatal("WebSearchAvailable() = true with nothing configured")
	}
}
