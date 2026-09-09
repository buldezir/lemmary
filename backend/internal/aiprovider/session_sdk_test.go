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
// never gets a value -- it is a request clone per attempt, and an earlier
// version of this read the context off the wrong one.
func TestSessionMiddlewareStampsThroughTheSDK(t *testing.T) {
	var seen string
	srv := completionServer(t, &seen)

	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
		option.WithMiddleware(SessionMiddleware()),
	)

	completeOnce(t, client, WithSession(context.Background(), "conv123"))

	if seen != "conv123" {
		t.Errorf("%s = %q, want %q", SessionHeader, seen, "conv123")
	}
}

// TestAClientWithoutTheMiddlewareSendsNoHeader is the other half: a provider
// that is not OpenCode never installs it, and that is the whole of the gate.
func TestAClientWithoutTheMiddlewareSendsNoHeader(t *testing.T) {
	var seen string
	srv := completionServer(t, &seen)

	client := openai.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL(srv.URL),
		option.WithMaxRetries(0),
	)

	completeOnce(t, client, WithSession(context.Background(), "conv123"))

	if seen != "" {
		t.Errorf("%s = %q, want empty without the middleware", SessionHeader, seen)
	}
}
