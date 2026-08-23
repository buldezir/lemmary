package appapi

import (
	"testing"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
)

func TestNeedsConfigSetup(t *testing.T) {
	t.Parallel()

	google := &aiprovider.Provider{SDK: aiprovider.SDKGoogleVision, APIKey: "g"}
	mistral := &aiprovider.Provider{SDK: aiprovider.SDKMistral, APIKey: "m"}
	openai := &aiprovider.Provider{SDK: aiprovider.SDKOpenAI, APIKey: "o"}

	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "ready google",
			cfg: config.Config{
				OCRProvider:     google,
				ExtractProvider: openai,
				ExtractModel:    "gpt",
			},
			want: false,
		},
		{
			name: "ready mistral",
			cfg: config.Config{
				OCRProvider:     mistral,
				OCRModel:        "mistral-ocr-latest",
				ExtractProvider: openai,
				ExtractModel:    "gpt",
			},
			want: false,
		},
		{
			name: "missing openai",
			cfg: config.Config{
				OCRProvider: google,
			},
			want: true,
		},
		{
			name: "missing google key",
			cfg: config.Config{
				OCRProvider:     &aiprovider.Provider{SDK: aiprovider.SDKGoogleVision},
				ExtractProvider: openai,
			},
			want: true,
		},
		{
			name: "missing mistral model",
			cfg: config.Config{
				OCRProvider:     mistral,
				ExtractProvider: openai,
			},
			want: true,
		},
		{
			name: "mistral used as llm",
			cfg: config.Config{
				OCRProvider:     google,
				ExtractProvider: mistral,
			},
			want: false,
		},
		{
			name: "ready mistral only",
			cfg: config.Config{
				OCRProvider:     mistral,
				OCRModel:        "mistral-ocr-latest",
				ExtractProvider: mistral,
			},
			want: false,
		},
		{
			name: "google used as llm",
			cfg: config.Config{
				OCRProvider:     google,
				ExtractProvider: google,
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := needsConfigSetup(tt.cfg); got != tt.want {
				t.Fatalf("needsConfigSetup() = %v, want %v", got, tt.want)
			}
		})
	}
}
