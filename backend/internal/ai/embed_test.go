package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/openai/openai-go/option"

	"lemmary/backend/internal/aiprovider"
)

type embedRequestBody struct {
	Input []string `json:"input"`
	Model string   `json:"model"`
}

// embedServer answers /embeddings, recording every request it saw.
type embedServer struct {
	mu       sync.Mutex
	requests []embedRequestBody

	// dims is the vector length to answer with; respond overrides everything.
	dims    int
	respond func(w http.ResponseWriter, r *http.Request, body embedRequestBody, n int) bool
}

func newEmbedServer(t *testing.T, srv *embedServer) *httptest.Server {
	t.Helper()
	if srv.dims == 0 {
		srv.dims = 3
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This server is not OpenCode, so the session header must not be sent.
		if got := r.Header.Get(aiprovider.SessionHeader); got != "" {
			t.Errorf("%s sent to a non-OpenCode host: %q", aiprovider.SessionHeader, got)
		}
		raw, _ := io.ReadAll(r.Body)
		var body embedRequestBody
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request: %v (%s)", err, raw)
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		srv.mu.Lock()
		srv.requests = append(srv.requests, body)
		n := len(srv.requests)
		srv.mu.Unlock()

		if srv.respond != nil && srv.respond(w, r, body, n) {
			return
		}
		writeEmbeddings(w, len(body.Input), srv.dims, false)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func (s *embedServer) seen() []embedRequestBody {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]embedRequestBody(nil), s.requests...)
}

func writeEmbeddings(w http.ResponseWriter, count, dims int, reversed bool) {
	type item struct {
		Object    string    `json:"object"`
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	}
	items := make([]item, 0, count)
	for i := 0; i < count; i++ {
		vec := make([]float64, dims)
		for d := range vec {
			// The value encodes the index, so a misassembled batch is visible.
			vec[d] = float64(i) + float64(d)/100
		}
		items = append(items, item{Object: "embedding", Index: i, Embedding: vec})
	}
	if reversed {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"object": "list",
		"model":  "test-embed",
		"data":   items,
		"usage":  map[string]int{"prompt_tokens": 10 * count, "total_tokens": 10 * count},
	})
}

func newTestEmbedder(t *testing.T, baseURL string, dims int) *openAIEmbedder {
	t.Helper()
	e, ok := NewEmbedder("openai", "test-key", "test-embed", baseURL, dims, 5*time.Second, slog.Default()).(*openAIEmbedder)
	if !ok {
		t.Fatal("NewEmbedder did not return the OpenAI implementation")
	}
	// No real backoff in tests: the schedule is asserted separately.
	e.sleep = func(time.Duration) {}
	return e
}

func inputs(n int, text string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = text
	}
	return out
}

func TestEmbedSplitsBatchesByInputCount(t *testing.T) {
	t.Parallel()
	srv := &embedServer{}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	result, err := e.Embed(t.Context(), inputs(200, "hello"))
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(result.Vectors) != 200 {
		t.Fatalf("got %d vectors, want 200", len(result.Vectors))
	}
	if result.Requests != 4 {
		t.Fatalf("Requests = %d, want 4 (64+64+64+8)", result.Requests)
	}

	seen := srv.seen()
	want := []int{64, 64, 64, 8}
	if len(seen) != len(want) {
		t.Fatalf("server saw %d requests, want %d", len(seen), len(want))
	}
	for i, n := range want {
		if len(seen[i].Input) != n {
			t.Fatalf("request %d carried %d inputs, want %d", i, len(seen[i].Input), n)
		}
	}
	// 10 tokens per input, reported per request and summed by the caller.
	if result.PromptTokens != 2000 {
		t.Fatalf("PromptTokens = %d, want 2000", result.PromptTokens)
	}
}

// A batch of long chunks blows the per-request token ceiling long before it
// reaches 64 inputs.
func TestEmbedSplitsBatchesByTotalLength(t *testing.T) {
	t.Parallel()
	srv := &embedServer{}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	long := strings.Repeat("x", 8000)
	if _, err := e.Embed(t.Context(), inputs(40, long)); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	seen := srv.seen()
	if len(seen) < 3 {
		t.Fatalf("server saw %d requests; 40 x 8000 runes must not be one", len(seen))
	}
	for i, req := range seen {
		total := 0
		for _, in := range req.Input {
			total += utf8.RuneCountInString(in)
		}
		if total > maxBatchRunes {
			t.Fatalf("request %d carried %d runes, over the %d ceiling", i, total, maxBatchRunes)
		}
	}
}

// The response is documented as unordered; reassembling by position would
// attach every vector to the wrong chunk without anything erroring.
func TestEmbedReassemblesByIndex(t *testing.T) {
	t.Parallel()
	srv := &embedServer{
		respond: func(w http.ResponseWriter, _ *http.Request, body embedRequestBody, _ int) bool {
			writeEmbeddings(w, len(body.Input), 3, true)
			return true
		},
	}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	result, err := e.Embed(t.Context(), inputs(5, "hello"))
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	for i, vec := range result.Vectors {
		if vec[0] != float32(i) {
			t.Fatalf("vector %d = %v; the batch was reassembled by position", i, vec)
		}
	}
}

func TestEmbedDetectsDimensionsFromTheFirstResponse(t *testing.T) {
	t.Parallel()
	srv := &embedServer{dims: 1536}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	if e.Dims() != 0 {
		t.Fatalf("Dims() = %d before the first call, want 0", e.Dims())
	}
	if _, err := e.Embed(t.Context(), inputs(2, "hello")); err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if e.Dims() != 1536 {
		t.Fatalf("Dims() = %d, want 1536", e.Dims())
	}
}

// Rows of mixed length are dropped silently by the vector index, so a provider
// that changes its answer has to stop the run rather than write half of it.
func TestEmbedRefusesAChangedDimensionCount(t *testing.T) {
	t.Parallel()
	srv := &embedServer{}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 1536)

	_, err := e.Embed(t.Context(), inputs(1, "hello"))
	if !errors.Is(err, ErrDimsMismatch) {
		t.Fatalf("Embed err = %v, want ErrDimsMismatch", err)
	}
	if e.Dims() != 1536 {
		t.Fatalf("Dims() = %d; the recorded length must not be overwritten", e.Dims())
	}
}

func TestEmbedRetriesRateLimitsAndServerErrors(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusBadGateway} {
		srv := &embedServer{
			respond: func(w http.ResponseWriter, _ *http.Request, _ embedRequestBody, n int) bool {
				if n < 3 {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
					return true
				}
				return false
			},
		}
		ts := newEmbedServer(t, srv)
		e := newTestEmbedder(t, ts.URL, 0)

		result, err := e.Embed(t.Context(), inputs(2, "hello"))
		if err != nil {
			t.Fatalf("status %d: Embed: %v", status, err)
		}
		if len(result.Vectors) != 2 {
			t.Fatalf("status %d: got %d vectors", status, len(result.Vectors))
		}
		if n := len(srv.seen()); n != 3 {
			t.Fatalf("status %d: server saw %d requests, want 3", status, n)
		}
		// Requests counts batches, not attempts: it is what the caller logs as
		// the document's cost.
		if result.Requests != 1 {
			t.Fatalf("status %d: Requests = %d, want 1", status, result.Requests)
		}
	}
}

func TestEmbedGivesUpAfterFourAttempts(t *testing.T) {
	t.Parallel()
	srv := &embedServer{
		respond: func(w http.ResponseWriter, _ *http.Request, _ embedRequestBody, _ int) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		},
	}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	if _, err := e.Embed(t.Context(), inputs(1, "hello")); err == nil {
		t.Fatal("Embed should fail once the attempts are spent")
	}
	if n := len(srv.seen()); n != embedMaxAttempts {
		t.Fatalf("server saw %d requests, want %d", n, embedMaxAttempts)
	}
}

// A 400 is an answer, not an outage; repeating it only spends the backoff.
func TestEmbedFailsFastOnClientErrors(t *testing.T) {
	t.Parallel()
	srv := &embedServer{
		respond: func(w http.ResponseWriter, _ *http.Request, _ embedRequestBody, _ int) bool {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown model"}}`))
			return true
		},
	}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	if _, err := e.Embed(t.Context(), inputs(1, "hello")); err == nil {
		t.Fatal("Embed should fail on a 400")
	}
	if n := len(srv.seen()); n != 1 {
		t.Fatalf("server saw %d requests, want exactly 1", n)
	}
}

func TestEmbedBackoffGrowsFourfold(t *testing.T) {
	t.Parallel()
	srv := &embedServer{
		respond: func(w http.ResponseWriter, _ *http.Request, _ embedRequestBody, _ int) bool {
			w.WriteHeader(http.StatusInternalServerError)
			return true
		},
	}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	var slept []time.Duration
	e.sleep = func(d time.Duration) { slept = append(slept, d) }

	_, _ = e.Embed(t.Context(), inputs(1, "hello"))

	want := []time.Duration{time.Second, 4 * time.Second, 16 * time.Second}
	if len(slept) != len(want) {
		t.Fatalf("slept %v, want %v", slept, want)
	}
	for i := range want {
		if slept[i] != want[i] {
			t.Fatalf("backoff %d = %v, want %v", i, slept[i], want[i])
		}
	}
}

func TestEmbedTruncatesAnOverlongInput(t *testing.T) {
	t.Parallel()
	srv := &embedServer{}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	if _, err := e.Embed(t.Context(), []string{strings.Repeat("ї", 9000)}); err != nil {
		t.Fatalf("Embed: %v", err)
	}

	seen := srv.seen()
	if len(seen) != 1 {
		t.Fatalf("server saw %d requests", len(seen))
	}
	if n := utf8.RuneCountInString(seen[0].Input[0]); n != maxInputRunes {
		t.Fatalf("input was %d runes, want it truncated to %d", n, maxInputRunes)
	}
}

// Chunk ordinals are positional, so a silently skipped input would misalign
// every vector after the gap.
func TestEmbedRefusesBlankInput(t *testing.T) {
	t.Parallel()
	srv := &embedServer{}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	if _, err := e.Embed(t.Context(), []string{"hello", "  \n "}); err == nil {
		t.Fatal("Embed should refuse a blank input")
	}
	if n := len(srv.seen()); n != 0 {
		t.Fatalf("server saw %d requests; the check must happen before any call", n)
	}
}

func TestEmbedNoInputsIsNoRequest(t *testing.T) {
	t.Parallel()
	srv := &embedServer{}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	result, err := e.Embed(t.Context(), nil)
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(result.Vectors) != 0 || result.Requests != 0 {
		t.Fatalf("result = %+v, want empty", result)
	}
	if n := len(srv.seen()); n != 0 {
		t.Fatalf("server saw %d requests", n)
	}
}

func TestEmbedRejectsAShortResponse(t *testing.T) {
	t.Parallel()
	srv := &embedServer{
		respond: func(w http.ResponseWriter, _ *http.Request, body embedRequestBody, _ int) bool {
			writeEmbeddings(w, len(body.Input)-1, 3, false)
			return true
		},
	}
	ts := newEmbedServer(t, srv)
	e := newTestEmbedder(t, ts.URL, 0)

	if _, err := e.Embed(t.Context(), inputs(3, "hello")); err == nil {
		t.Fatal("Embed should refuse a response with fewer vectors than inputs")
	}
}

func TestEmbedderReportsNameAndModel(t *testing.T) {
	t.Parallel()
	e := NewEmbedder("mistral", "k", " mistral-embed ", "https://api.mistral.ai/v1/", 0, time.Second, nil)

	if e.Name() != "mistral" {
		t.Fatalf("Name() = %q", e.Name())
	}
	if e.Model() != "mistral-embed" {
		t.Fatalf("Model() = %q", e.Model())
	}
}

// TestEmbedSendsSessionHeaderToOpenCode is the production-client case: dropping
// SessionMiddleware from NewEmbedder must fail this, not only the hand-built
// SDK wiring test.
func TestEmbedSendsSessionHeaderToOpenCode(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(aiprovider.SessionHeader)
		writeEmbeddings(w, 1, 3, false)
	}))
	t.Cleanup(srv.Close)

	e, ok := NewEmbedder("openai", "test-key", "test-embed",
		"http://opencode.ai/zen/go/v1", 0, 5*time.Second, slog.Default(),
		option.WithMiddleware(aiprovider.RewriteHostMiddleware(srv.Listener.Addr().String())),
	).(*openAIEmbedder)
	if !ok {
		t.Fatal("NewEmbedder did not return the OpenAI implementation")
	}
	e.sleep = func(time.Duration) {}

	ctx := aiprovider.WithSession(context.Background(), "conv123")
	if _, err := e.Embed(ctx, []string{"hello"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if seen != "conv123" {
		t.Errorf("%s = %q, want %q", aiprovider.SessionHeader, seen, "conv123")
	}
}
