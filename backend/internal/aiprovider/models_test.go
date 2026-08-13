package aiprovider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChatCompletionsURL(t *testing.T) {
	t.Parallel()
	got := ChatCompletionsURL("https://opencode.ai/zen/go/v1/")
	if got != "https://opencode.ai/zen/go/v1/chat/completions" {
		t.Fatalf("ChatCompletionsURL() = %q", got)
	}
	if got := ChatCompletionsURL(""); got != "https://api.openai.com/v1/chat/completions" {
		t.Fatalf("default ChatCompletionsURL() = %q", got)
	}
}

func TestLogRequestNilLogger(t *testing.T) {
	t.Parallel()
	LogRequest(nil, SDKOpenAI, "POST", "https://example.test/v1/chat/completions", "m", "purpose", "chat")
}

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
	models, err := parseModelsResponse([]byte(`{"data":[{"id":"gpt-4o","name":"GPT-4o"},{"id":"other"},{"model":"skip-me"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("len=%d want 3: %+v", len(models), models)
	}
	if models[0].ID != "gpt-4o" || models[0].Name != "GPT-4o" {
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

func TestListModelsMistralOCRFiltersByOCRSubstring(t *testing.T) {
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
	if len(all) != 4 {
		t.Fatalf("unfiltered len=%d want 4: %+v", len(all), all)
	}
}

func TestFilterModelsBySubstring(t *testing.T) {
	t.Parallel()
	in := []Model{
		{ID: "mistral-ocr-latest", Name: "mistral-ocr-latest"},
		{ID: "codestral-latest", Name: "codestral-latest"},
		{ID: "other", Name: "Has OCR in name"},
	}
	got := filterModelsBySubstring(in, "ocr")
	if len(got) != 2 || got[0].ID != "mistral-ocr-latest" || got[1].ID != "other" {
		t.Fatalf("got %+v", got)
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
