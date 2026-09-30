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
	// refuse, and reading it as empty would silently run the configured model.
	partial := Overrides{Chat: aiprovider.Binding{Model: "gpt-6-astra"}}
	if partial.Empty() {
		t.Fatal("a model with no provider must not read as no override")
	}
	full := Overrides{Chat: aiprovider.Binding{ProviderID: "p1", Model: "gpt-6-astra"}}
	if full.Empty() {
		t.Fatal("a populated binding must not read as no override")
	}
}

// An override dropped by a missing case in Purposes would run on the configured
// model with nothing to show it had been ignored.
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

// A chunk row records its model and the index only reads rows matching the
// configured spec, so a re-embed on another model writes rows nobody reads.
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
			// The message has to name the model to re-embed on; an admin cannot act on
			// "wrong model".
			if err != nil && tc.configured != "" && !strings.Contains(err.Error(), tc.configured) {
				t.Fatalf("error does not name the configured model: %v", err)
			}
		})
	}
}

func TestConfiguredInAcceptsOnlyWhatSettingsBinds(t *testing.T) {
	t.Parallel()
	cfg := Config{
		OCRProviderID: "ocr", OCRModel: "ocr-m",
		ExtractProviderID: "llm", ExtractModel: "small",
		ResearchProviderID: "llm", ResearchModel: "big",
		EmbeddingProviderID: "emb", EmbeddingModel: "emb-m",
	}
	general := aiprovider.Binding{ProviderID: "llm", Model: "small"}
	research := aiprovider.Binding{ProviderID: "llm", Model: "big"}
	elsewhere := aiprovider.Binding{ProviderID: "llm", Model: "frontier"}

	for name, tc := range map[string]struct {
		o    Overrides
		want bool
	}{
		"nothing picked":                    {Overrides{}, true},
		"chat on the general model":         {Overrides{Chat: general}, true},
		"chat on the research model":        {Overrides{Chat: research}, false},
		"search on the research model":      {Overrides{Search: research}, true},
		"search on the general model":       {Overrides{Search: general}, true},
		"search elsewhere":                  {Overrides{Search: elsewhere}, false},
		"extract on the general model":      {Overrides{Extract: general}, true},
		"extract elsewhere":                 {Overrides{Extract: elsewhere}, false},
		"ocr on the ocr binding":            {Overrides{OCR: aiprovider.Binding{ProviderID: "ocr", Model: "ocr-m"}}, true},
		"ocr on the language model":         {Overrides{OCR: general}, false},
		"embedding on the embedding model":  {Overrides{Embedding: aiprovider.Binding{ProviderID: "emb", Model: "emb-m"}}, true},
		"embedding on another provider":     {Overrides{Embedding: aiprovider.Binding{ProviderID: "llm", Model: "emb-m"}}, false},
		"whitespace is not another binding": {Overrides{Chat: aiprovider.Binding{ProviderID: " llm ", Model: " small "}}, true},
	} {
		got := true
		for _, item := range tc.o.Purposes() {
			got = got && item.configuredIn(cfg)
		}
		if got != tc.want {
			t.Errorf("%s: configuredIn = %v, want %v", name, got, tc.want)
		}
	}
}

// Not parallel: the managed flag is process-global.
func TestValidateRefusesAnotherModelWhenManaged(t *testing.T) {
	prev := aiprovider.Managed()
	t.Cleanup(func() { aiprovider.SetManaged(prev) })
	aiprovider.SetManaged(true)

	cfg := Config{ExtractProviderID: "llm", ExtractModel: "small"}
	err := (Overrides{Chat: aiprovider.Binding{ProviderID: "llm", Model: "frontier"}}).Validate(nil, cfg)
	if err == nil || !strings.Contains(err.Error(), "managed") {
		t.Fatalf("Validate = %v, want a managed refusal", err)
	}
}
