package config

import (
	"strings"
	"testing"

	"lemmary/backend/internal/aiprovider"
)

func TestOverridesEmpty(t *testing.T) {
	t.Parallel()
	if !(Overrides{}).Empty() {
		t.Fatal("the zero Overrides overrides nothing")
	}
	// A model with no provider is not empty: it is a request Validate has to
	// refuse. Reading it as empty would run the configured model instead of the
	// one that was asked for, silently.
	partial := Overrides{Chat: aiprovider.Binding{Model: "gpt-6-astra"}}
	if partial.Empty() {
		t.Fatal("a model with no provider must not read as no override")
	}
	full := Overrides{Chat: aiprovider.Binding{ProviderID: "p1", Model: "gpt-6-astra"}}
	if full.Empty() {
		t.Fatal("a populated binding must not read as no override")
	}
}

// Every binding has to be reachable: an override dropped by a missing case in
// Purposes would run on the configured model with nothing to show it had been
// ignored.
func TestOverridesPurposesCoversEveryBinding(t *testing.T) {
	t.Parallel()
	o := Overrides{
		Chat:      aiprovider.Binding{ProviderID: "chat"},
		Search:    aiprovider.Binding{ProviderID: "search"},
		Extract:   aiprovider.Binding{ProviderID: "extract"},
		OCR:       aiprovider.Binding{ProviderID: "ocr"},
		Embedding: aiprovider.Binding{ProviderID: "embedding"},
	}
	seen := map[string]aiprovider.ModelPurpose{}
	for _, item := range o.Purposes() {
		seen[item.Binding.ProviderID] = item.Purpose
	}
	want := map[string]aiprovider.ModelPurpose{
		"chat":      aiprovider.PurposeLLM,
		"search":    aiprovider.PurposeLLM,
		"extract":   aiprovider.PurposeLLM,
		"ocr":       aiprovider.PurposeOCR,
		"embedding": aiprovider.PurposeEmbedding,
	}
	if len(seen) != len(want) {
		t.Fatalf("Purposes() covered %d bindings, want %d: %+v", len(seen), len(want), seen)
	}
	for name, purpose := range want {
		if seen[name] != purpose {
			t.Fatalf("%s binding validated as %q, want %q", name, seen[name], purpose)
		}
	}
}

// The guard that keeps a re-embed from spending a provider call on vectors
// nothing will ever read: a chunk row records its model, and the index only
// reads rows matching the configured spec.
func TestValidateEmbeddingModel(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		configured string
		override   aiprovider.Binding
		wantErr    bool
	}{
		"no override is fine": {
			configured: "text-embedding-3-small",
		},
		"the configured model is a legitimate re-embed": {
			configured: "text-embedding-3-small",
			override:   aiprovider.Binding{ProviderID: "p1", Model: "text-embedding-3-small"},
		},
		"whitespace does not make it a different model": {
			configured: "text-embedding-3-small",
			override:   aiprovider.Binding{ProviderID: "p1", Model: "  text-embedding-3-small  "},
		},
		"another model writes rows the index discards": {
			configured: "text-embedding-3-small",
			override:   aiprovider.Binding{ProviderID: "p1", Model: "text-embedding-3-large"},
			wantErr:    true,
		},
		"no index at all": {
			configured: "",
			override:   aiprovider.Binding{ProviderID: "p1", Model: "text-embedding-3-small"},
			wantErr:    true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := Overrides{Embedding: tc.override}
			err := o.validateEmbeddingModel(Config{EmbeddingModel: tc.configured})
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateEmbeddingModel() error = %v, wantErr %v", err, tc.wantErr)
			}
			// The message has to name the model to re-embed on; an admin cannot
			// act on "wrong model".
			if err != nil && tc.configured != "" && !strings.Contains(err.Error(), tc.configured) {
				t.Fatalf("error does not name the configured model: %v", err)
			}
		})
	}
}
