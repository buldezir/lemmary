package websearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type tavilyCapture struct {
	calls  atomic.Int64
	path   string
	method string
	auth   string
	agent  string
	body   map[string]any
}

// newTavilyServer answers every call with body, recording what it was sent.
func newTavilyServer(t *testing.T, status int, body string) (*httptest.Server, *tavilyCapture) {
	t.Helper()
	capture := &tavilyCapture{body: map[string]any{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.calls.Add(1)
		capture.path = r.URL.Path
		capture.method = r.Method
		capture.auth = r.Header.Get("Authorization")
		capture.agent = r.Header.Get("User-Agent")

		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		if err := json.Unmarshal(raw, &capture.body); err != nil {
			t.Errorf("decode request body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, capture
}

func TestSearchSendsTheQueryAndReturnsRankedResults(t *testing.T) {
	server, capture := newTavilyServer(t, http.StatusOK, `{
		"results": [
			{"title": "VAT rates", "url": "https://example.com/vat", "content": "The standard rate is 19%."},
			{"title": "", "url": "https://example.org/b", "content": "  spaced  "},
			{"title": "no url", "url": "  ", "content": "dropped"}
		]
	}`)

	client := NewTavily("tvly-key", server.URL, 5*time.Second, nil)
	results, err := client.Search(context.Background(), "  german vat rate  ", 3)
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	if got := capture.method; got != http.MethodPost {
		t.Errorf("method = %q, want POST", got)
	}
	if got := capture.path; got != "/search" {
		t.Errorf("path = %q, want /search", got)
	}
	if got := capture.auth; got != "Bearer tvly-key" {
		t.Errorf("Authorization = %q, want a bearer key", got)
	}
	// A hand-rolled request has no middleware to catch the loss of this.
	if got := capture.agent; got != "Lemmary" {
		t.Errorf("User-Agent = %q, want Lemmary", got)
	}
	if got := capture.body["query"]; got != "german vat rate" {
		t.Errorf("query = %v, want the trimmed query", got)
	}
	if got := capture.body["max_results"]; got != float64(3) {
		t.Errorf("max_results = %v, want 3", got)
	}
	if got := capture.body["search_depth"]; got != "basic" {
		t.Errorf("search_depth = %v, want basic", got)
	}
	// The snippet is the cheap first look; the page is what Fetch is for.
	if _, ok := capture.body["include_raw_content"]; ok {
		t.Error("include_raw_content must not be requested")
	}

	// The third result has no URL and is not a result.
	if len(results) != 2 {
		t.Fatalf("results = %d, want 2", len(results))
	}
	if got := results[0]; got.Title != "VAT rates" || got.URL != "https://example.com/vat" || got.Snippet != "The standard rate is 19%." {
		t.Errorf("results[0] = %+v", got)
	}
	if got := results[1].Snippet; got != "spaced" {
		t.Errorf("results[1].Snippet = %q, want it trimmed", got)
	}
}

func TestSearchDefaultsTheResultCount(t *testing.T) {
	server, capture := newTavilyServer(t, http.StatusOK, `{"results":[]}`)

	client := NewTavily("k", server.URL, 5*time.Second, nil)
	if _, err := client.Search(context.Background(), "q", 0); err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if got := capture.body["max_results"]; got != float64(5) {
		t.Errorf("max_results = %v, want the default 5", got)
	}
}

func TestSearchRefusesAnEmptyQueryWithoutCallingTheProvider(t *testing.T) {
	server, capture := newTavilyServer(t, http.StatusOK, `{"results":[]}`)

	client := NewTavily("k", server.URL, 5*time.Second, nil)
	if _, err := client.Search(context.Background(), "   ", 5); err == nil {
		t.Fatal("Search() with a blank query should fail")
	}
	if got := capture.calls.Load(); got != 0 {
		t.Errorf("calls = %d, want the provider left alone", got)
	}
}

func TestFetchReturnsPagesAndFoldsInTheFailedOnes(t *testing.T) {
	server, capture := newTavilyServer(t, http.StatusOK, `{
		"results": [{"url": "https://example.com/a", "raw_content": "# Heading\n\nbody"}],
		"failed_results": [
			{"url": "https://example.com/b", "error": "timed out"},
			{"url": "https://example.com/c", "error": "  "}
		]
	}`)

	client := NewTavily("k", server.URL, 5*time.Second, nil)
	pages, err := client.Fetch(context.Background(), []string{"https://example.com/a", "  ", "https://example.com/b"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}

	if got := capture.path; got != "/extract" {
		t.Errorf("path = %q, want /extract", got)
	}
	if got := capture.body["format"]; got != "markdown" {
		t.Errorf("format = %v, want markdown", got)
	}
	urls, _ := capture.body["urls"].([]any)
	if len(urls) != 2 {
		t.Fatalf("urls = %v, want the blank one dropped", capture.body["urls"])
	}

	if len(pages) != 3 {
		t.Fatalf("pages = %d, want one read and two failed", len(pages))
	}
	if got := pages[0]; got.URL != "https://example.com/a" || got.Content != "# Heading\n\nbody" || got.Error != "" {
		t.Errorf("pages[0] = %+v", got)
	}
	if got := pages[1]; got.Error != "timed out" || got.Content != "" {
		t.Errorf("pages[1] = %+v, want the provider's reason", got)
	}
	// A failure with no reason still has to read as a failure.
	if got := pages[2].Error; got != "could not be read" {
		t.Errorf("pages[2].Error = %q, want a fallback reason", got)
	}
}

func TestFetchTruncatesALongPage(t *testing.T) {
	long := strings.Repeat("x", maxPageBytes+500)
	body, err := json.Marshal(map[string]any{
		"results": []map[string]any{{"url": "https://example.com/a", "raw_content": long}},
	})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	server, _ := newTavilyServer(t, http.StatusOK, string(body))

	client := NewTavily("k", server.URL, 5*time.Second, nil)
	pages, err := client.Fetch(context.Background(), []string{"https://example.com/a"})
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if got := len(pages[0].Content); got != maxPageBytes {
		t.Errorf("page bytes = %d, want %d", got, maxPageBytes)
	}
}

func TestFetchRefusesAnEmptyURLList(t *testing.T) {
	server, capture := newTavilyServer(t, http.StatusOK, `{"results":[]}`)

	client := NewTavily("k", server.URL, 5*time.Second, nil)
	if _, err := client.Fetch(context.Background(), []string{"  ", ""}); err == nil {
		t.Fatal("Fetch() with no usable urls should fail")
	}
	if got := capture.calls.Load(); got != 0 {
		t.Errorf("calls = %d, want the provider left alone", got)
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{"object detail", http.StatusUnauthorized, `{"detail":{"error":"Unauthorized: missing API key."}}`, "Unauthorized: missing API key."},
		{"string detail", http.StatusBadRequest, `{"detail":"query is required"}`, "query is required"},
		{"no envelope", http.StatusInternalServerError, `upstream exploded`, "upstream exploded"},
		{"status in the message", http.StatusTooManyRequests, `{"detail":"slow down"}`, "HTTP 429"},
		{"unreadable success", http.StatusOK, `not json`, "decode response"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newTavilyServer(t, tc.status, tc.body)
			client := NewTavily("k", server.URL, 5*time.Second, nil)
			_, err := client.Search(context.Background(), "q", 5)
			if err == nil {
				t.Fatal("Search() should fail")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantSub)
			}
			if !strings.Contains(err.Error(), "tavily search") {
				t.Errorf("error = %q, want it to name the provider and the call", err)
			}
		})
	}
}

func TestAMissingKeyFailsBeforeTheRequest(t *testing.T) {
	server, capture := newTavilyServer(t, http.StatusOK, `{"results":[]}`)

	client := NewTavily("   ", server.URL, 5*time.Second, nil)
	if _, err := client.Search(context.Background(), "q", 5); err == nil {
		t.Fatal("Search() without a key should fail")
	}
	if got := capture.calls.Load(); got != 0 {
		t.Errorf("calls = %d, want no unauthenticated request", got)
	}
}

func TestAnEmptyBaseURLFallsBackToTavilysOwnEndpoint(t *testing.T) {
	client := NewTavily("k", "", 5*time.Second, nil)
	if got := client.url("/search"); got != "https://api.tavily.com/search" {
		t.Errorf("url = %q, want Tavily's documented endpoint", got)
	}
	// A trailing slash must not double up.
	trimmed := NewTavily("k", "https://proxy.example/", 5*time.Second, nil)
	if got := trimmed.url("/extract"); got != "https://proxy.example/extract" {
		t.Errorf("url = %q", got)
	}
}
