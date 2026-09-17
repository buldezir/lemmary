package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"

	"lemmary/backend/internal/aiprovider"
)

// managedForTest turns AI_MANAGED on for the length of one test. Not
// parallel-safe; none of the tests below call t.Parallel().
func managedForTest(t *testing.T, on bool) {
	t.Helper()
	prev := aiprovider.Managed()
	aiprovider.SetManaged(on)
	t.Cleanup(func() { aiprovider.SetManaged(prev) })
}

func docHeaderServer(t *testing.T, seen *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Get(aiprovider.DocumentHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The gate on the client every completion goes through: the header rides the
// context in managed mode, and a self-hosted install never sends it.
func TestChatSendsDocumentHeaderOnlyWhenManaged(t *testing.T) {
	cases := []struct {
		name    string
		managed bool
		doc     string
		want    string
	}{
		{"managed, one document", true, "doc123", "doc123"},
		{"managed, no document", true, "", ""},
		{"self-hosted, one document", false, "doc123", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			managedForTest(t, tc.managed)
			var seen string
			srv := docHeaderServer(t, &seen)

			client := NewOpenAIClient(aiprovider.SDKOpenAI, "k", "test-model", srv.URL, "", "", 5*time.Second, slog.Default())
			ctx := aiprovider.WithDocument(context.Background(), tc.doc)
			if _, err := client.Chat(ctx, "ocr text", []ChatMessage{{Role: "user", Content: "hi"}}, nil); err != nil {
				t.Fatalf("chat: %v", err)
			}
			if seen != tc.want {
				t.Errorf("%s = %q, want %q", aiprovider.DocumentHeader, seen, tc.want)
			}
		})
	}
}

// The /messages third of the OpenCode catalogue speaks a second SDK, so it
// needs its own middleware.
func TestOpenCodeMessagesSendsDocumentHeader(t *testing.T) {
	managedForTest(t, true)
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(aiprovider.DocumentHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant",
			"model": "minimax-m3", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "ok"}},
			"usage":   map[string]any{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKOpenCode, "k", "minimax-m3", srv.URL+"/zen/go/v1", "", "", 5*time.Second, slog.Default())
	ctx := aiprovider.WithDocument(context.Background(), "doc123")
	if _, err := client.Complete(ctx, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if seen != "doc123" {
		t.Errorf("%s = %q, want %q", aiprovider.DocumentHeader, seen, "doc123")
	}
}

// Embedding is the one binding that batches: a long document is several
// requests, and every one of them is about the same document.
func TestEmbedSendsDocumentHeaderOnEveryBatch(t *testing.T) {
	managedForTest(t, true)
	var seen []string
	srv := &embedServer{}
	ts := newEmbedServer(t, srv)
	inner := ts.Config.Handler
	ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get(aiprovider.DocumentHeader))
		inner.ServeHTTP(w, r)
	})

	embedder := NewEmbedder(aiprovider.SDKOpenAI, "k", "test-model", ts.URL, 0, 5*time.Second, slog.Default())
	ctx := aiprovider.WithDocument(context.Background(), "doc123")
	if _, err := embedder.Embed(ctx, []string{"one", "two"}); err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(seen) == 0 {
		t.Fatal("the embedding endpoint was never called")
	}
	for i, got := range seen {
		if got != "doc123" {
			t.Errorf("request %d: %s = %q, want %q", i, aiprovider.DocumentHeader, got, "doc123")
		}
	}
}

// Archive-wide work is about no single document, so nothing may invent one.
func TestSearchHelperSendsNoDocumentHeader(t *testing.T) {
	managedForTest(t, true)
	var seen string
	srv := docHeaderServer(t, &seen)

	client := NewOpenAIClient(aiprovider.SDKOpenAI, "k", "test-model", srv.URL, "", "", 5*time.Second, slog.Default())
	if _, err := client.Complete(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("test-model"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if seen != "" {
		t.Errorf("%s sent for an archive-wide request: %q", aiprovider.DocumentHeader, seen)
	}
}
