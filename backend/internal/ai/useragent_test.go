package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/aiprovider"
)

// TestChatUserAgent pins both halves of the rule on the client every
// completion goes through: an ordinary provider hears our name, and the Codex
// backend -- which vets its callers -- still hears the SDK's.
func TestChatUserAgent(t *testing.T) {
	for _, tc := range []struct {
		sdk  string
		want string
	}{
		{aiprovider.SDKOpenAI, aiprovider.UserAgent},
		{aiprovider.SDKOpenCode, aiprovider.UserAgent},
		{aiprovider.SDKChatGPT, "OpenAI/Go"},
	} {
		t.Run(tc.sdk, func(t *testing.T) {
			var seen string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.Header.Get("User-Agent")
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
			defer srv.Close()

			client := NewOpenAIClient(tc.sdk, "test-key", "test-model", srv.URL, "", "", 5*time.Second, slog.Default())
			if _, err := client.Chat(context.Background(), "text", []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
				t.Fatalf("chat: %v", err)
			}
			if !strings.HasPrefix(seen, tc.want) {
				t.Errorf("User-Agent = %q, want prefix %q", seen, tc.want)
			}
		})
	}
}
