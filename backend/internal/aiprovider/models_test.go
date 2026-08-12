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
	models, err := ListModels(t.Context(), p, true, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "google/gemini-flash" {
		t.Fatalf("got %+v", models)
	}
}

func TestListModelsGoogleVisionEmpty(t *testing.T) {
	t.Parallel()
	models, err := ListModels(t.Context(), Provider{SDK: SDKGoogleVision, APIKey: "x"}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 0 {
		t.Fatalf("got %+v", models)
	}
}
