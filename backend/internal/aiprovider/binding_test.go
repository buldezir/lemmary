package aiprovider

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
)

func TestBindingEmpty(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		binding Binding
		want    bool
	}{
		"zero":               {Binding{}, true},
		"whitespace only":    {Binding{ProviderID: "  "}, true},
		"provider":           {Binding{ProviderID: "p1"}, false},
		"provider and model": {Binding{ProviderID: "p1", Model: "gpt-6-astra"}, false},
		// Empty asks only whether a provider was named, which is what its
		// callers -- the per-binding build guards in config.WithOverrides --
		// need. A model with no provider reads as empty here and is refused by
		// Resolve; config.Overrides.Empty is the one that must not use this
		// answer, because it short-circuits before Resolve runs.
		"model only": {Binding{Model: "gpt-6-astra"}, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := tc.binding.Empty(); got != tc.want {
				t.Fatalf("Empty() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBindingNormalized(t *testing.T) {
	t.Parallel()
	got := Binding{ProviderID: "  p1 ", Model: "\tgpt-6-astra\n"}.Normalized()
	if got != (Binding{ProviderID: "p1", Model: "gpt-6-astra"}) {
		t.Fatalf("Normalized() = %+v", got)
	}
}

func TestServesPurpose(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		sdk     string
		purpose ModelPurpose
		want    bool
	}{
		"openai chats":                {SDKOpenAI, PurposeLLM, true},
		"openai reads documents":      {SDKOpenAI, PurposeOCR, true},
		"openai embeds":               {SDKOpenAI, PurposeEmbedding, true},
		"chatgpt chats":               {SDKChatGPT, PurposeLLM, true},
		"chatgpt cannot embed":        {SDKChatGPT, PurposeEmbedding, false},
		"google vision only reads":    {SDKGoogleVision, PurposeOCR, true},
		"google vision cannot chat":   {SDKGoogleVision, PurposeLLM, false},
		"docling only reads":          {SDKDocling, PurposeOCR, true},
		"docling cannot embed":        {SDKDocling, PurposeEmbedding, false},
		"local embeddings only embed": {SDKLocalEmbeddings, PurposeEmbedding, true},
		"local cannot chat":           {SDKLocalEmbeddings, PurposeLLM, false},
		"local cannot read":           {SDKLocalEmbeddings, PurposeOCR, false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := ServesPurpose(tc.sdk, tc.purpose); got != tc.want {
				t.Fatalf("ServesPurpose(%q, %q) = %v, want %v", tc.sdk, tc.purpose, got, tc.want)
			}
		})
	}
}

// The lists an error message is built from have to agree with the predicate it
// is explaining, which is why they are derived rather than written out.
func TestPurposeSDKsMatchTheDerivedLists(t *testing.T) {
	t.Parallel()
	if got, want := PurposeSDKs(PurposeLLM), LLMSDKs(); !slices.Equal(got, want) {
		t.Fatalf("PurposeSDKs(llm) = %v, want %v", got, want)
	}
	if got, want := PurposeSDKs(PurposeOCR), OCRSDKs(); !slices.Equal(got, want) {
		t.Fatalf("PurposeSDKs(ocr) = %v, want %v", got, want)
	}
	if got, want := PurposeSDKs(PurposeEmbedding), EmbeddingSDKs(); !slices.Equal(got, want) {
		t.Fatalf("PurposeSDKs(embedding) = %v, want %v", got, want)
	}
}

func bootAppForBinding(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	if _, err := EnsureCollection(app); err != nil {
		t.Fatalf("ensure %s: %v", CollectionName, err)
	}
	return app
}

func saveProvider(t *testing.T, app core.App, sdk, alias, apiKey, baseURL string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		t.Fatalf("%s collection: %v", CollectionName, err)
	}
	record := core.NewRecord(collection)
	record.Set("sdk", sdk)
	record.Set("alias", alias)
	record.Set("api_key", apiKey)
	record.Set("base_url", baseURL)
	if err := app.Save(record); err != nil {
		t.Fatalf("save provider %q: %v", alias, err)
	}
	return record.Id
}

// Resolve is the trust boundary: the provider id arrives from a browser, on
// endpoints any signed-in user may call, and it decides which of the operator's
// credentials a request spends.
func TestResolve(t *testing.T) {
	app := bootAppForBinding(t)

	openAI := saveProvider(t, app, SDKOpenAI, "OpenAI", "sk-test", "https://api.openai.com/v1")
	keyless := saveProvider(t, app, SDKOpenAI, "OpenAI unconfigured", "", "https://api.openai.com/v1")
	// A sidecar is configured by address alone, with no key to check.
	sidecar := saveProvider(t, app, SDKLocalEmbeddings, "Local embeddings", "", "http://embeddings:80/v1")
	defaulted := saveProvider(t, app, SDKDocling, "Docling on its default address", "", "")

	cases := map[string]struct {
		binding Binding
		purpose ModelPurpose
		wantErr bool
	}{
		"empty binding is not an error": {
			binding: Binding{},
			purpose: PurposeLLM,
		},
		"a capable configured provider": {
			binding: Binding{ProviderID: openAI, Model: "gpt-6-astra"},
			purpose: PurposeLLM,
		},
		// The model is not checked against the catalogue: Settings has always
		// had a "Custom model id" field for the model a provider added last
		// week, and a wrong name surfaces as a provider error on first use.
		"a model absent from any catalogue": {
			binding: Binding{ProviderID: openAI, Model: "gpt-nonesuch-9"},
			purpose: PurposeLLM,
		},
		"an unknown provider id": {
			binding: Binding{ProviderID: "nosuchprovider", Model: "gpt-6-astra"},
			purpose: PurposeLLM,
			wantErr: true,
		},
		"a provider with no credential": {
			binding: Binding{ProviderID: keyless, Model: "gpt-6-astra"},
			purpose: PurposeLLM,
			wantErr: true,
		},
		// Configured, keyless, and still wrong for this binding: the SDK that
		// embeds without chatting is the one a capability check has to catch,
		// because a key check never would.
		"a configured sidecar bound to chat": {
			binding: Binding{ProviderID: sidecar, Model: "bge-m3"},
			purpose: PurposeLLM,
			wantErr: true,
		},
		"the same sidecar bound to embedding": {
			binding: Binding{ProviderID: sidecar, Model: "bge-m3"},
			purpose: PurposeEmbedding,
		},
		// A sidecar row saved with a blank address is still configured:
		// FromRecord normalizes it to DefaultBaseURL, which is the compose
		// service name. So there is no "unreachable sidecar" to refuse here --
		// an address that answers nothing is a request error, not a binding one.
		"a sidecar left on its default address": {
			binding: Binding{ProviderID: defaulted, Model: "rapidocr"},
			purpose: PurposeOCR,
		},
		"a model with no provider": {
			binding: Binding{Model: "gpt-6-astra"},
			purpose: PurposeLLM,
			wantErr: true,
		},
		// The other half-filled pair. An empty model reaches the provider as an
		// empty model, and the configured one is no fallback: it belongs to a
		// different provider, which need not serve it at all.
		"a provider with no model": {
			binding: Binding{ProviderID: openAI},
			purpose: PurposeLLM,
			wantErr: true,
		},
		"an embedding provider with no model": {
			binding: Binding{ProviderID: sidecar},
			purpose: PurposeEmbedding,
			wantErr: true,
		},
		// The exception: these two read a document without being told a model,
		// so blank is the correct configuration rather than a missing field.
		"an OCR SDK that needs no model": {
			binding: Binding{ProviderID: defaulted},
			purpose: PurposeOCR,
		},
		"an OCR SDK that does need one": {
			binding: Binding{ProviderID: openAI},
			purpose: PurposeOCR,
			wantErr: true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			p, model, err := Resolve(app, tc.binding, tc.purpose)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Resolve() error = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil {
				return
			}
			if tc.binding.Empty() {
				if p != nil {
					t.Fatalf("Resolve() of an empty binding returned provider %+v", p)
				}
				return
			}
			if p == nil {
				t.Fatal("Resolve() returned no provider and no error")
			}
			if model != tc.binding.Normalized().Model {
				t.Fatalf("model = %q, want %q", model, tc.binding.Model)
			}
		})
	}
}
