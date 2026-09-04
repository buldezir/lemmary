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
			name:     "paddleocr needs neither key nor model",
			provider: aiprovider.Provider{SDK: aiprovider.SDKPaddleOCR, Alias: "PaddleOCR", BaseURL: "http://paddleocr:8080"},
			wantName: aiprovider.SDKPaddleOCR,
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
