package aiprovider

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelsURLOpenRouterOCRAddsFileFilter(t *testing.T) {
	t.Parallel()
	p := Provider{SDK: SDKOpenRouter, BaseURL: "https://openrouter.ai/api/v1"}
	got := ModelsURL(p, PurposeOCR)
	if got != "https://openrouter.ai/api/v1/models?input_modalities=file" {
		t.Fatalf("ModelsURL() = %q", got)
	}
}

func TestModelsURLOpenAIOCRUnfiltered(t *testing.T) {
	t.Parallel()
	p := Provider{SDK: SDKOpenAI, BaseURL: "https://api.openai.com/v1"}
	got := ModelsURL(p, PurposeOCR)
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
		if got := r.Header.Get(SessionHeader); got != "" {
			t.Errorf("%s sent to a non-OpenCode host: %q", SessionHeader, got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"google/gemini-flash"}]}`))
	}))
	defer srv.Close()

	p := Provider{SDK: SDKOpenRouter, BaseURL: srv.URL + "/v1", APIKey: "test-key"}
	models, err := ListModels(t.Context(), p, PurposeOCR, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "google/gemini-flash" {
		t.Fatalf("got %+v", models)
	}
}

// rewriteHostTransport delivers a ListModels request to an httptest server
// after SessionHost has already seen opencode.ai and stamped the header.
type rewriteHostTransport struct{ host string }

func (t rewriteHostTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Host = t.host
	clone.Host = t.host
	return http.DefaultTransport.RoundTrip(clone)
}

// TestListModelsSendsSessionHeaderToOpenCode is the hand-rolled path: ListModels
// does not go through the SDK middleware, so dropping the header set there
// would stay green without this.
func TestListModelsSendsSessionHeaderToOpenCode(t *testing.T) {
	t.Parallel()
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(SessionHeader)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"kimi-k3"}]}`))
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: rewriteHostTransport{host: srv.Listener.Addr().String()}}
	p := Provider{SDK: SDKOpenAI, BaseURL: "http://opencode.ai/zen/go/v1", APIKey: "test-key"}
	models, err := ListModels(t.Context(), p, PurposeLLM, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "kimi-k3" {
		t.Fatalf("got %+v", models)
	}
	if want := SessionFor("models"); seen != want {
		t.Errorf("%s = %q, want the models purpose id %q", SessionHeader, seen, want)
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
	ocr, err := ListModels(t.Context(), p, PurposeOCR, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(ocr) != 1 || ocr[0].ID != "mistral-ocr-latest" {
		t.Fatalf("ocr catalog=%+v", ocr)
	}

	llm, err := ListModels(t.Context(), p, PurposeLLM, srv.Client(), nil)
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
	models, err := ListModels(t.Context(), p, PurposeOCR, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("len=%d want 2: %+v", len(models), models)
	}
	if models[0].ID != "mistral-ocr-latest" || models[1].ID != "mistral-ocr-2512" {
		t.Fatalf("got %+v", models)
	}

	all, err := ListModels(t.Context(), p, PurposeLLM, srv.Client(), nil)
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

	ocr := filterModels(in, SDKMistral, PurposeOCR)
	if len(ocr) != 3 || ocr[0].ID != "mistral-ocr-latest" || ocr[1].ID != "legacy-ocr" || ocr[2].ID != "named" {
		t.Fatalf("ocr=%+v", ocr)
	}

	llm := filterModels(in, SDKMistral, PurposeLLM)
	if len(llm) != 2 || llm[0].ID != "invoice-ocr-extract" || llm[1].ID != "codestral-latest" {
		t.Fatalf("llm=%+v", llm)
	}

	// Mistral has no embeddings capability flag, so the embedding model is the
	// one whose capabilities object claims neither chat nor OCR.
	embed := filterModels(in, SDKMistral, PurposeEmbedding)
	if len(embed) != 1 || embed[0].ID != "mistral-embed" {
		t.Fatalf("embed=%+v", embed)
	}
}

// Binding an embedding model to extraction or OCR fails on every document, so
// the two lists it must never appear in are the two an admin picks from most.
func TestFilterModelsKeepsEmbeddingModelsOutOfLLMAndOCR(t *testing.T) {
	t.Parallel()
	in := []Model{
		{ID: "gpt-5.6-luna", Name: "gpt-5.6-luna"},
		{ID: "text-embedding-3-small", Name: "text-embedding-3-small"},
	}

	for _, purpose := range []ModelPurpose{PurposeLLM, PurposeOCR} {
		got := filterModels(in, SDKOpenAI, purpose)
		if len(got) != 1 || got[0].ID != "gpt-5.6-luna" {
			t.Fatalf("for=%s: %+v", purpose, got)
		}
	}

	embed := filterModels(in, SDKOpenAI, PurposeEmbedding)
	if len(embed) != 1 || embed[0].ID != "text-embedding-3-small" {
		t.Fatalf("embed=%+v", embed)
	}
}

// OpenRouter states the modality outright, which beats the name heuristic: it
// keeps a chat model whose name happens to contain "embed" out of the
// embeddings list.
func TestFilterModelsUsesOpenRouterOutputModalities(t *testing.T) {
	t.Parallel()
	in := []Model{
		{ID: "vendor/embedder-chat", Name: "Embedder Chat", outputModalities: []string{"text"}},
		{ID: "vendor/qwen3", Name: "Qwen3", outputModalities: []string{"embeddings"}},
	}

	embed := filterModels(in, SDKOpenRouter, PurposeEmbedding)
	if len(embed) != 1 || embed[0].ID != "vendor/qwen3" {
		t.Fatalf("embed=%+v", embed)
	}
	llm := filterModels(in, SDKOpenRouter, PurposeLLM)
	if len(llm) != 1 || llm[0].ID != "vendor/embedder-chat" {
		t.Fatalf("llm=%+v", llm)
	}
}

func TestParseModelPurposeDefaultsToLLM(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]ModelPurpose{
		"":          PurposeLLM,
		"llm":       PurposeLLM,
		"nonsense":  PurposeLLM,
		"ocr":       PurposeOCR,
		" OCR ":     PurposeOCR,
		"embedding": PurposeEmbedding,
		"Embedding": PurposeEmbedding,
	} {
		if got := ParseModelPurpose(raw); got != want {
			t.Fatalf("ParseModelPurpose(%q) = %q, want %q", raw, got, want)
		}
	}
}

// OpenRouter's plain catalogue omits embedding models entirely; only the
// output_modalities filter brings them back. Other SDKs have no such filter
// and stay on the bare endpoint.
func TestModelsURLEmbeddingFiltersOpenRouterByOutputModality(t *testing.T) {
	t.Parallel()
	p := Provider{SDK: SDKOpenRouter, BaseURL: "https://openrouter.ai/api/v1"}
	if got := ModelsURL(p, PurposeEmbedding); got != "https://openrouter.ai/api/v1/models?output_modalities=embeddings" {
		t.Fatalf("ModelsURL() = %q", got)
	}

	other := Provider{SDK: SDKOpenAI, BaseURL: "https://api.openai.com/v1"}
	if got := ModelsURL(other, PurposeEmbedding); got != "https://api.openai.com/v1/models" {
		t.Fatalf("ModelsURL() = %q", got)
	}
	if got := ModelsURL(p, PurposeLLM); got != "https://openrouter.ai/api/v1/models" {
		t.Fatalf("LLM listing should stay unfiltered, got %q", got)
	}
}

func TestListModelsGoogleVisionEmpty(t *testing.T) {
	t.Parallel()
	models, err := ListModels(t.Context(), Provider{SDK: SDKGoogleVision, APIKey: "x"}, PurposeOCR, nil, nil)
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

// text-embeddings-inference does not document a /v1/models, so the local
// catalogue is /info -- which sits beside the /v1 prefix, not under it.
func TestModelsURLLocalUsesInfo(t *testing.T) {
	t.Parallel()
	p := Provider{SDK: SDKLocal, BaseURL: "http://embeddings:80/v1"}
	for _, purpose := range []ModelPurpose{PurposeEmbedding, PurposeLLM, PurposeOCR} {
		if got := ModelsURL(p, purpose); got != "http://embeddings:80/info" {
			t.Fatalf("ModelsURL(%s) = %q", purpose, got)
		}
	}
	// A base URL written without the /v1 suffix still resolves.
	bare := Provider{SDK: SDKLocal, BaseURL: "http://embeddings:80"}
	if got := ModelsURL(bare, PurposeEmbedding); got != "http://embeddings:80/info" {
		t.Fatalf("ModelsURL(bare) = %q", got)
	}
}

func TestParseInfoResponse(t *testing.T) {
	t.Parallel()
	// model_type is a tagged union: an object in some TEI versions, a bare
	// string in others. Both have shipped and both mean the same thing.
	cases := []struct {
		name string
		body string
		want string
		dims int
	}{
		{
			name: "object model_type",
			body: `{"model_id":"BAAI/bge-m3","served_model_name":"BAAI/bge-m3","model_type":{"embedding":{"pooling":"cls"}},"max_input_length":8192}`,
			want: "BAAI/bge-m3",
			dims: 8192,
		},
		{
			name: "string model_type",
			body: `{"model_id":"thenlper/gte-base","served_model_name":"thenlper/gte-base","model_type":"embedding","max_input_length":512}`,
			want: "thenlper/gte-base",
			dims: 512,
		},
		{
			name: "served name wins over model id",
			body: `{"model_id":"/data/models--BAAI--bge-m3","served_model_name":"bge","model_type":"embedding"}`,
			want: "bge",
		},
		{
			name: "model id when nothing is served under a name",
			body: `{"model_id":"BAAI/bge-m3","model_type":"embedding"}`,
			want: "BAAI/bge-m3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models, err := parseInfoResponse([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if len(models) != 1 {
				t.Fatalf("len=%d want 1: %+v", len(models), models)
			}
			if models[0].ID != tc.want {
				t.Fatalf("id=%q want %q", models[0].ID, tc.want)
			}
			if models[0].ContextWindow != tc.dims {
				t.Fatalf("context window=%d want %d", models[0].ContextWindow, tc.dims)
			}
		})
	}
}

// TEI serves rerankers and classifiers from the same image and the same
// endpoint shape. Either one bound as an embedding model would fail on every
// document with nothing in the UI to explain why, so an unrecognised type
// yields no models rather than a guess.
func TestParseInfoResponseRefusesANonEmbeddingModel(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		`{"model_id":"BAAI/bge-reranker-v2-m3","model_type":{"reranker":{}},"max_input_length":512}`,
		`{"model_id":"some/classifier","model_type":"classifier"}`,
		`{"model_id":"mystery","model_type":null}`,
	} {
		models, err := parseInfoResponse([]byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if len(models) != 0 {
			t.Fatalf("%s: got %+v, want none", body, models)
		}
	}
}

// The local picker must not go through the name heuristic: "BAAI/bge-m3"
// carries no "embed" for it to find, so asking it would empty the one picker
// this SDK exists for. And a keyless provider must send no Authorization header
// at all rather than a blank Bearer.
func TestListModelsLocalReadsInfoWithoutAKey(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/info" {
			http.NotFound(w, r)
			return
		}
		if _, ok := r.Header["Authorization"]; ok {
			t.Errorf("a keyless provider sent an Authorization header")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model_id":"BAAI/bge-m3","served_model_name":"BAAI/bge-m3","model_type":{"embedding":{"pooling":"cls"}},"max_input_length":8192,"max_client_batch_size":64}`))
	}))
	defer srv.Close()

	p := Provider{SDK: SDKLocal, BaseURL: srv.URL + "/v1"}

	models, err := ListModels(t.Context(), p, PurposeEmbedding, srv.Client(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "BAAI/bge-m3" {
		t.Fatalf("embedding models = %+v", models)
	}

	// It cannot chat or read a document, so it must offer nothing to those
	// pickers even though /info answers the same way for all three.
	for _, purpose := range []ModelPurpose{PurposeLLM, PurposeOCR} {
		models, err := ListModels(t.Context(), p, purpose, srv.Client(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(models) != 0 {
			t.Fatalf("%s models = %+v, want none", purpose, models)
		}
	}
}
