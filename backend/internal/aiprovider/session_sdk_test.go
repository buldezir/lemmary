package aiprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

// completionServer answers one chat completion and records the session header
// it was sent.
func completionServer(t *testing.T, seen *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = r.Header.Get(SessionHeader)
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

func completeOnce(t *testing.T, client openai.Client, ctx context.Context) {
	t.Helper()
	_, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("test-model"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("completion: %v", err)
	}
}

// TestSessionMiddlewareStampsThroughTheSDK is the end-to-end case: the SDK must
// hand the middleware a request carrying the caller's context, or the header
// never gets a value. The base URL names opencode.ai so the gate opens, and a
// second middleware -- running after ours -- redirects the connection to the
// test server rather than the internet.
func TestSessionMiddlewareStampsThroughTheSDK(t *testing.T) {
	var seen string
	srv := completionServer(t, &seen)

	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("http://opencode.ai/zen/go/v1"),
		option.WithMaxRetries(0),
		option.WithMiddleware(SessionMiddleware()),
		option.WithMiddleware(RewriteHostMiddleware(srv.Listener.Addr().String())),
	)

	completeOnce(t, client, WithSession(context.Background(), "conv123"))

	if seen != "conv123" {
		t.Errorf("%s = %q, want %q", SessionHeader, seen, "conv123")
	}
}

// TestSessionMiddlewareSkipsOtherProvidersThroughTheSDK is the same wiring
// against a base URL that is not OpenCode: nothing is added to the request.
func TestSessionMiddlewareSkipsOtherProvidersThroughTheSDK(t *testing.T) {
	var seen string
	srv := completionServer(t, &seen)

	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
		option.WithMiddleware(SessionMiddleware()),
	)

	completeOnce(t, client, WithSession(context.Background(), "conv123"))

	if seen != "" {
		t.Errorf("%s = %q, want empty for a non-OpenCode host", SessionHeader, seen)
	}
}
