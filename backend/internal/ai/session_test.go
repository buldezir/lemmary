package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openai/openai-go/option"

	"lemmary/backend/internal/aiprovider"
)

func chatCompletionServer(t *testing.T, seen *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Get(aiprovider.SessionHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-1",
			"object":  "chat.completion",
			"created": 1,
			"model":   "test-model",
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

// TestChatSendsSessionHeaderToOpenCode is the production-client case the
// middleware unit tests cannot cover: NewOpenAIClient itself must install
// SessionMiddleware. The base URL names opencode.ai so the gate opens;
// RewriteHostMiddleware (test-only, after ours) sends the bytes to httptest.
func TestChatSendsSessionHeaderToOpenCode(t *testing.T) {
	var seen string
	srv := chatCompletionServer(t, &seen)

	client := NewOpenAIClient("openai", "test-key", "test-model",
		"http://opencode.ai/zen/go/v1", "", "", 5*time.Second, slog.Default(),
		option.WithMiddleware(aiprovider.RewriteHostMiddleware(srv.Listener.Addr().String())),
	)
	ctx := aiprovider.WithSession(context.Background(), "conv123")
	if _, err := client.Chat(ctx, "some ocr text", []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if seen != "conv123" {
		t.Errorf("%s = %q, want %q", aiprovider.SessionHeader, seen, "conv123")
	}
}

// TestChatSendsNoSessionHeaderToOtherProviders pins the gate on the client
// every completion in the app goes through: a provider that is not OpenCode
// sees the request it has always seen, session on the context or not.
func TestChatSendsNoSessionHeaderToOtherProviders(t *testing.T) {
	var seen string
	srv := chatCompletionServer(t, &seen)

	client := NewOpenAIClient("openai", "test-key", "test-model", srv.URL, "", "", 5*time.Second, slog.Default())
	ctx := aiprovider.WithSession(context.Background(), "conv123")
	if _, err := client.Chat(ctx, "some ocr text", []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("chat: %v", err)
	}
	if seen != "" {
		t.Errorf("%s sent to a non-OpenCode host: %q", aiprovider.SessionHeader, seen)
	}
}
