package appapi

import (
	"testing"

	"paperless-go/backend/internal/config"
)

func TestNeedsConfigSetup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "ready google",
			cfg: config.Config{
				OCRProvider:        "google_vision",
				GoogleVisionAPIKey: "g",
				OpenAIAPIKey:       "o",
			},
			want: false,
		},
		{
			name: "ready mistral",
			cfg: config.Config{
				OCRProvider:   "mistral",
				MistralAPIKey: "m",
				OpenAIAPIKey:  "o",
			},
			want: false,
		},
		{
			name: "missing openai",
			cfg: config.Config{
				OCRProvider:        "google_vision",
				GoogleVisionAPIKey: "g",
			},
			want: true,
		},
		{
			name: "missing google key",
			cfg: config.Config{
				OCRProvider:  "google_vision",
				OpenAIAPIKey: "o",
			},
			want: true,
		},
		{
			name: "missing mistral key",
			cfg: config.Config{
				OCRProvider:  "mistral",
				OpenAIAPIKey: "o",
			},
			want: true,
		},
		{
			name: "wrong provider key ignored",
			cfg: config.Config{
				OCRProvider:        "mistral",
				GoogleVisionAPIKey: "g",
				OpenAIAPIKey:       "o",
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
