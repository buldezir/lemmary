package aiprovider

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func catalogServer(t *testing.T, body string) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if r.URL.Path != "/openai" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

const oneModel = `{"gpt-4o":{"contextWindow":128000,"maxTokens":16384}}`

func TestCatalogReportsTheContextWindow(t *testing.T) {
	t.Parallel()
	srv, _ := catalogServer(t, oneModel)
	catalog := NewCatalog(srv.URL, quietLogger())

	if got := catalog.ContextWindow(context.Background(), "openai", "gpt-4o"); got != 128000 {
		t.Fatalf("window = %d, want 128000", got)
	}
}

// Every way of not knowing reads the same: zero, never an error, because the
// number is decoration on a turn that runs regardless.
func TestCatalogAnswersZeroForEveryUnknown(t *testing.T) {
	t.Parallel()
	srv, calls := catalogServer(t, oneModel)

	cases := []struct {
		name             string
		baseURL          string
		catalogID, model string
	}{
		{"model not listed", srv.URL, "openai", "gpt-9-imaginary"},
		{"provider not tagged", srv.URL, "", "gpt-4o"},
		{"no model", srv.URL, "openai", ""},
		{"catalogue disabled", "", "openai", "gpt-4o"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			catalog := NewCatalog(tc.baseURL, quietLogger())
			if got := catalog.ContextWindow(context.Background(), tc.catalogID, tc.model); got != 0 {
				t.Fatalf("window = %d, want 0", got)
			}
		})
	}

	// A disabled catalogue must not reach the network at all, and neither must
	// a lookup with nothing to look up.
	if before := atomic.LoadInt32(calls); before > 1 {
		t.Fatalf("requests = %d, want at most the one real lookup", before)
	}
}

func TestCatalogCachesOneFetchPerProvider(t *testing.T) {
	t.Parallel()
	srv, calls := catalogServer(t, oneModel)
	catalog := NewCatalog(srv.URL, quietLogger())

	for i := 0; i < 5; i++ {
		if got := catalog.ContextWindow(context.Background(), "openai", "gpt-4o"); got != 128000 {
			t.Fatalf("window = %d on lookup %d", got, i)
		}
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Fatalf("requests = %d, want 1: the answer is cached", got)
	}

	// Past its TTL the same lookup goes back out.
	catalog.mu.Lock()
	catalog.entries["openai"] = catalogEntry{models: catalog.entries["openai"].models, expiresAt: time.Now().Add(-time.Minute)}
	catalog.mu.Unlock()
	catalog.ContextWindow(context.Background(), "openai", "gpt-4o")
	if got := atomic.LoadInt32(calls); got != 2 {
		t.Fatalf("requests = %d, want a refetch once the entry expired", got)
	}
}

// An unreachable host must not cost a request per turn, so the failure itself
// is cached for a while.
func TestCatalogDoesNotRetryAFailedFetchImmediately(t *testing.T) {
	t.Parallel()
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	catalog := NewCatalog(srv.URL, quietLogger())
	for i := 0; i < 3; i++ {
		if got := catalog.ContextWindow(context.Background(), "openai", "gpt-4o"); got != 0 {
			t.Fatalf("window = %d, want 0 when the catalogue is down", got)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("requests = %d, want 1: a failure is cached too", got)
	}
}

func TestDefaultCatalogAndValidCatalog(t *testing.T) {
	t.Parallel()
	if got := DefaultCatalog(SDKOpenAI); got != "openai" {
		t.Fatalf("openai SDK defaulted to %q", got)
	}
	if got := DefaultCatalog(SDKDocling); got != "" {
		t.Fatalf("a sidecar SDK serves no models, got %q", got)
	}
	if !ValidCatalog("") {
		t.Fatal("empty must be accepted: it means no window is known")
	}
	if !ValidCatalog("groq") {
		t.Fatal("groq is in the hardcoded list")
	}
	if ValidCatalog("not-a-catalogue") {
		t.Fatal("an unknown catalogue must be refused")
	}
}
