package ai

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTranslateSendsTheWholeTextInOneCall(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		calls.Add(1)
		body = string(raw)
		writeChatJSON(w, " translated \n")
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient("openai", "test-key", "mistral-small-latest", srv.URL, "v1", "de", 5*time.Second, slog.Default())
	long := strings.Repeat("Absatz. ", 20000) + "ENDE"
	got, err := client.Translate(context.Background(), long)
	if err != nil {
		t.Fatalf("Translate: %v", err)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
	if got != "translated" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(body, "ENDE") {
		t.Fatal("the text must be sent untruncated")
	}
	if !strings.Contains(body, `\"de\"`) {
		t.Fatalf("system prompt should name the result language, got %s", body)
	}
}

func TestTranslateRefusesAReplyCutOffAtTheOutputLimit(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"test","choices":[{"index":0,"finish_reason":"length","message":{"role":"assistant","content":"half a transl"}}]}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient("openai", "test-key", "mistral-small-latest", srv.URL, "v1", "de", 5*time.Second, slog.Default())
	if _, err := client.Translate(context.Background(), "Invoice"); !errors.Is(err, ErrTranslationTruncated) {
		t.Fatalf("err = %v, want ErrTranslationTruncated", err)
	}
}
