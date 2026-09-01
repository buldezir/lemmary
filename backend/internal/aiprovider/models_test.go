package aiprovider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsURLOpenRouterOCRAddsFileFilter(t *testing.T) {
	t.Parallel()
	p := Provider{SDK: SDKOpenRouter, BaseURL: "https://openrouter.ai/api/v1"}
	got := ModelsURL(p, true)
	if got != "https://openrouter.ai/api/v1/models?input_modalities=file" {
		t.Fatalf("ModelsURL() = %q", got)
	}
}

func TestModelsURLOpenAIOCRUnfiltered(t *testing.T) {
	t.Parallel()
	p := Provider{SDK: SDKOpenAI, BaseURL: "https://api.openai.com/v1"}
	got := ModelsURL(p, true)
	if got != "https://api.openai.com/v1/models" {
		t.Fatalf("ModelsURL() = %q", got)
	}
}

func TestParseModelsResponse(t *testing.T) {
	t.Parallel()
	models, err := parseModelsResponse([]byte(`{"data":[{"id":"gpt-5.6-luna","name":"GPT-5.6 Luna"},{"id":"other"},{"model":"skip-me"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("len=%d want 3: %+v", len(models), models)
	}
	if models[0].ID != "gpt-5.6-luna" || models[0].Name != "GPT-5.6 Luna" {
		t.Fatalf("first=%+v", models[0])
	}
	if models[2].ID != "skip-me" {
		t.Fatalf("third=%+v", models[2])
	}
}

func TestParseModelsResponseOpenCodeGoCatalog(t *testing.T) {
	t.Parallel()
	body := []byte(`{"object":"list","data":[{"id":"minimax-m3","object":"model","created":1786645321,"owned_by":"opencode"},{"id":"deepseek-v4-flash","object":"model","created":1786645321,"owned_by":"opencode"}]}`)
	models, err := parseModelsResponse(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(models), models)
	}
	if models[0].ID != "minimax-m3" || models[0].Name != "minimax-m3" {
		t.Fatalf("first=%+v", models[0])
	}
	if models[1].ID != "deepseek-v4-flash" {
		t.Fatalf("second=%+v", models[1])
	}
}

func TestParseModelsResponseDedupes(t *testing.T) {
	t.Parallel()
	models, err := parseModelsResponse([]byte(`{"data":[{"id":"a"},{"id":"a","name":"A"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "a" {
		t.Fatalf("got %+v", models)
	}
}

func TestListModels(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("input_modalities") != "file" {
			http.Error(w, "missing filter", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"google/gemini-flash"}]}`))
	}))
	defer srv.Close()

	p := Provider{SDK: SDKOpenRouter, BaseURL: srv.URL + "/v1", APIKey: "test-key"}
	models, err := ListModels(t.Context(), p, true, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "google/gemini-flash" {
		t.Fatalf("got %+v", models)
	}
}

func TestListModelsMistralFiltersByCapabilities(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"mistral-small-latest","capabilities":{"completion_chat":true,"function_calling":true,"ocr":false}},
			{"id":"mistral-ocr-latest","capabilities":{"completion_chat":false,"ocr":true}},
			{"id":"mistral-embed","capabilities":{"completion_chat":false,"ocr":false}},
			{"id":"mistral-moderation-latest","capabilities":{"completion_chat":false,"moderation":true,"ocr":false}},
			{"id":"codestral-embed","capabilities":{"completion_chat":false,"ocr":false}},
			{"id":"invoice-ocr-extract","capabilities":{"completion_chat":true,"ocr":false}}
		]}`))
	}))
	defer srv.Close()

	p := Provider{SDK: SDKMistral, BaseURL: srv.URL + "/v1", APIKey: "test-key"}
	ocr, err := ListModels(t.Context(), p, true, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ocr) != 1 || ocr[0].ID != "mistral-ocr-latest" {
		t.Fatalf("ocr catalog=%+v", ocr)
	}

	llm, err := ListModels(t.Context(), p, false, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(llm) != 2 {
		t.Fatalf("llm catalog len=%d want 2: %+v", len(llm), llm)
	}
	if llm[0].ID != "mistral-small-latest" || llm[1].ID != "invoice-ocr-extract" {
		t.Fatalf("llm catalog=%+v", llm)
	}
}

func TestListModelsMistralFallsBackToOCRSubstring(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"mistral-small-latest"},
			{"id":"mistral-ocr-latest"},
			{"id":"mistral-ocr-2512","name":"Mistral OCR"},
			{"id":"codestral-latest"}
		]}`))
	}))
	defer srv.Close()

	p := Provider{SDK: SDKMistral, BaseURL: srv.URL + "/v1", APIKey: "test-key"}
	models, err := ListModels(t.Context(), p, true, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(models), models)
	}
	if models[0].ID != "mistral-ocr-latest" || models[1].ID != "mistral-ocr-2512" {
		t.Fatalf("got %+v", models)
	}

	all, err := ListModels(t.Context(), p, false, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("llm catalog len=%d want 2: %+v", len(all), all)
	}
	if all[0].ID != "mistral-small-latest" || all[1].ID != "codestral-latest" {
		t.Fatalf("llm catalog=%+v", all)
	}
}

func TestFilterMistralModelsMixesCapabilitiesAndSubstringFallback(t *testing.T) {
	t.Parallel()
	in := []Model{
		{ID: "mistral-ocr-latest", Name: "mistral-ocr-latest", caps: &modelCapabilities{ocr: true}},
		{ID: "mistral-embed", Name: "mistral-embed", caps: &modelCapabilities{}},
		{ID: "invoice-ocr-extract", Name: "invoice-ocr-extract", caps: &modelCapabilities{completionChat: true}},
		{ID: "legacy-ocr", Name: "legacy-ocr"},
		{ID: "codestral-latest", Name: "codestral-latest"},
		{ID: "named", Name: "Has OCR in name"},
	}

	ocr := filterMistralModels(in, true)
	if len(ocr) != 3 || ocr[0].ID != "mistral-ocr-latest" || ocr[1].ID != "legacy-ocr" || ocr[2].ID != "named" {
		t.Fatalf("ocr=%+v", ocr)
	}

	llm := filterMistralModels(in, false)
	if len(llm) != 2 || llm[0].ID != "invoice-ocr-extract" || llm[1].ID != "codestral-latest" {
		t.Fatalf("llm=%+v", llm)
	}
}

func TestListModelsGoogleVisionEmpty(t *testing.T) {
	t.Parallel()
	models, err := ListModels(t.Context(), Provider{SDK: SDKGoogleVision, APIKey: "x"}, true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("got %+v", models)
	}
}

func TestParseModelsResponseReadsContextWindow(t *testing.T) {
	t.Parallel()
	// OpenRouter reports context_length (and repeats it under top_provider),
	// Mistral reports max_context_length, OpenAI reports neither.
	models, err := parseModelsResponse([]byte(`{"data":[
		{"id":"openrouter","context_length":200000},
		{"id":"openrouter-detail","top_provider":{"context_length":131072}},
		{"id":"openrouter-both","context_length":200000,"top_provider":{"context_length":131072}},
		{"id":"openrouter-both-reversed","context_length":131072,"top_provider":{"context_length":200000}},
		{"id":"mistral","max_context_length":32768},
		{"id":"openai"}
	]}`))
	if err != nil {
		t.Fatalf("parseModelsResponse: %v", err)
	}

	want := map[string]int{
		"openrouter":        200000,
		"openrouter-detail": 131072,
		// Both keys on one model is the normal OpenRouter listing shape, and
		// the smaller number wins whichever field carries it: top_provider is
		// the window of the provider a request is actually routed to, and
		// research spends this number until it is gone. Advertising the model
		// maximum here means the completion is rejected mid-run.
		"openrouter-both":          131072,
		"openrouter-both-reversed": 131072,
		"mistral":                  32768,
		"openai":                   0,
	}
	if len(models) != len(want) {
		t.Fatalf("models = %d, want %d", len(models), len(want))
	}
	for _, m := range models {
		if got := m.ContextWindow; got != want[m.ID] {
			t.Fatalf("%s context window = %d, want %d", m.ID, got, want[m.ID])
		}
	}
}

func TestPickContextWindowTakesTheFirstPositiveValue(t *testing.T) {
	t.Parallel()
	if got := pickContextWindow(0, 0, 4096); got != 4096 {
		t.Fatalf("got %d, want 4096", got)
	}
	if got := pickContextWindow(8192, 4096); got != 8192 {
		t.Fatalf("got %d, want 8192", got)
	}
	if got := pickContextWindow(0, 0); got != 0 {
		t.Fatalf("got %d, want 0 for an unreported window", got)
	}
}

func TestSmallestContextWindowIgnoresMissingValues(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		values []int
		want   int
	}{
		{"both positive", []int{200000, 131072}, 131072},
		{"either order", []int{131072, 200000}, 131072},
		{"only one reported", []int{0, 131072}, 131072},
		{"only the other", []int{131072, 0}, 131072},
		{"none reported", []int{0, 0}, 0},
		{"negative is not a window", []int{-1, 8192}, 8192},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := smallestContextWindow(tc.values...); got != tc.want {
				t.Fatalf("smallestContextWindow(%v) = %d, want %d", tc.values, got, tc.want)
			}
		})
	}
}
