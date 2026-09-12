package ocr

import (
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/aiprovider"
)

// TestNewFromAIProviderRequirements pins what each SDK must bring before a
// client is built. The keyed rows are regression guards: relaxing the key check
// for the sidecars must not relax it for anybody else.
func TestNewFromAIProviderRequirements(t *testing.T) {
	tests := []struct {
		name     string
		provider aiprovider.Provider
		model    string
		wantName string
		wantErr  string
	}{
		{
			name:     "docling needs neither key nor model",
			provider: aiprovider.Provider{SDK: aiprovider.SDKDocling, Alias: "Docling", BaseURL: "http://docling:5001"},
			wantName: aiprovider.SDKDocling,
		},
		{
			name:     "docling still needs an address",
			provider: aiprovider.Provider{SDK: aiprovider.SDKDocling, Alias: "Docling"},
			wantErr:  "base URL",
		},
		{
			name:     "mistral still needs a key",
			provider: aiprovider.Provider{SDK: aiprovider.SDKMistral, Alias: "Mistral"},
			model:    "mistral-ocr-latest",
			wantErr:  "API key",
		},
		{
			name:     "google vision still needs a key",
			provider: aiprovider.Provider{SDK: aiprovider.SDKGoogleVision, Alias: "Google"},
			wantErr:  "API key",
		},
		{
			name:     "openai still needs a model",
			provider: aiprovider.Provider{SDK: aiprovider.SDKOpenAI, Alias: "OpenAI", APIKey: "sk-test"},
			wantErr:  "OCR model is required",
		},
		{
			// The credential is a minted token, which config.providerCredential
			// substitutes for the key. Demanding an api_key here would refuse
			// a provider that is signed in and working.
			name: "chatgpt needs no key, only a model",
			provider: aiprovider.Provider{
				SDK: aiprovider.SDKChatGPT, Alias: "ChatGPT subscription",
				BaseURL: aiprovider.DefaultBaseURL(aiprovider.SDKChatGPT),
			},
			model:    "gpt-5.6-luna",
			wantName: aiprovider.SDKChatGPT,
		},
		{
			name: "chatgpt still needs a model",
			provider: aiprovider.Provider{
				SDK: aiprovider.SDKChatGPT, Alias: "ChatGPT subscription",
				BaseURL: aiprovider.DefaultBaseURL(aiprovider.SDKChatGPT),
			},
			wantErr: "OCR model is required",
		},
		{
			name:     "an unknown sdk is refused, not treated as keyless",
			provider: aiprovider.Provider{SDK: "tesseract", Alias: "Tesseract", BaseURL: "http://x"},
			wantErr:  "API key",
		},
		{
			name:     "an empty sdk is refused",
			provider: aiprovider.Provider{Alias: "Nothing"},
			wantErr:  "API key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			provider, err := NewFromAIProvider(tc.provider, tc.model, 5*time.Second, nil)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want an error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewFromAIProvider: %v", err)
			}
			if provider.Name() != tc.wantName {
				t.Errorf("Name() = %q, want %q", provider.Name(), tc.wantName)
			}
		})
	}
}

// TestWithMetricsKeepsConcurrencyLimit guards the one thing the timing wrapper
// could quietly break: pdfsplit chooses its fan-out width by asserting the
// provider to LimitedConcurrency, so a wrapper that hid docling's answer would
// aim a page per core at a sidecar running one worker -- and a wrapper that
// invented an answer for everybody else would serialise the hosted providers,
// which are the ones that want to be called in parallel.
func TestWithMetricsKeepsConcurrencyLimit(t *testing.T) {
	t.Parallel()

	limited := withMetrics(NewDoclingProvider("http://docling:5001", "", "", time.Second, nil), aiprovider.SDKDocling, "")
	got, ok := limited.(LimitedConcurrency)
	if !ok {
		t.Fatal("wrapped docling provider no longer implements LimitedConcurrency")
	}
	if n := got.MaxConcurrency(); n != 1 {
		t.Errorf("wrapped docling MaxConcurrency() = %d, want 1", n)
	}

	unlimited := withMetrics(NewMistralProvider("sk-test", "mistral-ocr-latest", "", time.Second, nil), aiprovider.SDKMistral, "mistral-ocr-latest")
	if _, ok := unlimited.(LimitedConcurrency); ok {
		t.Error("wrapped mistral provider now claims a concurrency limit it does not have")
	}
}
