package chatgpt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"lemmary/backend/internal/aiprovider"
)

// codexServer answers like the Codex backend: an SSE stream with no
// Content-Type header. It records the path and headers it was reached with.
func codexServer(t *testing.T, seen *http.Request, path *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = *r
		*path = r.URL.Path
		_, _ = w.Write([]byte(
			"event: x\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n" +
				"event: x\ndata: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n",
		))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The end-to-end case: the middleware sits under a real openai-go client, so
// the path it rewrites is the one the SDK actually built from the base URL.
//
// It is worth asserting through the SDK rather than by hand because
// option.WithBaseURL appends a trailing slash and resolves the endpoint as a
// relative reference. A base URL of ".../backend-api/codex" that lost its last
// segment on the way would post to /backend-api/responses and 404, and no unit
// test over a hand-built request would notice.
func TestTheMiddlewareRewritesThePathTheSDKBuilds(t *testing.T) {
	var seen http.Request
	var path string
	srv := codexServer(t, &seen, &path)

	// The host is httptest's; the path is the real one, which is the half this
	// test is about.
	client := openai.NewClient(
		option.WithAPIKey(PlaceholderKey),
		option.WithBaseURL(srv.URL+chatgptBasePath),
		option.WithMiddleware(Middleware(signedInSource(t, "sdk1"), nil)),
	)

	resp, err := client.Chat.Completions.New(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("gpt-5.6-luna"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("completion through the SDK: %v", err)
	}

	if path != chatgptBasePath+"/responses" {
		t.Fatalf("path = %q, want %s/responses", path, chatgptBasePath)
	}
	if got := seen.Header.Get("originator"); got != codexOriginator {
		t.Errorf("originator = %q, want %q", got, codexOriginator)
	}
	if got := seen.Header.Get("Authorization"); got != "Bearer access-1" {
		t.Errorf("Authorization = %q, want the live token rather than the placeholder", got)
	}

	// And the SDK decoded the reassembled answer, which is the whole point:
	// ai.CompleteChat and everything above it see an ordinary completion.
	if len(resp.Choices) != 1 || resp.Choices[0].Message.Content != "ok" {
		t.Fatalf("choices = %+v", resp.Choices)
	}
	if resp.Usage.PromptTokens != 2 || resp.Usage.CompletionTokens != 1 {
		t.Fatalf("usage = %+v", resp.Usage)
	}
}

// chatgptBasePath is the path half of DefaultBaseURL(SDKChatGPT), so the test
// above cannot drift from the URL the SDK is actually configured with.
var chatgptBasePath = "/backend-api/codex"

func TestTheDefaultBaseURLCarriesThatPath(t *testing.T) {
	t.Parallel()
	want := "https://chatgpt.com" + chatgptBasePath
	if got := aiprovider.DefaultBaseURL(aiprovider.SDKChatGPT); got != want {
		t.Fatalf("DefaultBaseURL = %q, want %q", got, want)
	}
}
