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

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"

	"lemmary/backend/internal/aiprovider"
)

// The wiring internal/opencode's own tests cannot cover: NewOpenAIClient has to
// build the Anthropic client, and build it against the right base URL.
// anthropic-sdk-go appends "v1/messages" itself, so a base URL left with its
// /v1 on would post to /zen/go/v1/v1/messages -- which no unit test over the
// translation would notice.
func TestAnOpenCodeMessagesModelReachesTheAnthropicEndpoint(t *testing.T) {
	var path string
	var session string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		session = r.Header.Get(aiprovider.SessionHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant",
			"model": "minimax-m3", "stop_reason": "end_turn",
			"content": []any{map[string]any{"type": "text", "text": "from messages"}},
			"usage":   map[string]any{"input_tokens": 6, "output_tokens": 2},
		})
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKOpenCode, "k", "minimax-m3",
		srv.URL+"/zen/go/v1", "", "", 5*time.Second, slog.Default())

	ctx := aiprovider.WithSession(context.Background(), "conv123")
	resp, err := client.Complete(ctx, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Choices[0].Message.Content != "from messages" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
	if path != "/zen/go/v1/messages" {
		t.Errorf("path = %q, want /zen/go/v1/messages", path)
	}
	if session != "conv123" {
		t.Errorf("%s = %q, want the conversation from the context", aiprovider.SessionHeader, session)
	}
	if u := usageOf(resp); u.Prompt != 6 || u.Completion != 2 {
		t.Errorf("usage = %+v, want 6 prompt and 2 completion", u)
	}
}

// The same wiring for the streamed path, which chat uses.
func TestAnOpenCodeMessagesModelStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range [][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"qwen3.8-max","content":[],"usage":{"input_tokens":4,"output_tokens":0}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi "}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"there"}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":4,"output_tokens":2}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			_, _ = w.Write([]byte("event: " + event[0] + "\ndata: " + event[1] + "\n\n"))
		}
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKOpenCode, "k", "qwen3.8-max",
		srv.URL+"/zen/go/v1", "", "", 5*time.Second, slog.Default())

	var deltas []string
	text, usage, err := client.completeStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("qwen3.8-max"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("completeStreaming: %v", err)
	}
	if text != "hi there" {
		t.Fatalf("text = %q", text)
	}
	if strings.Join(deltas, "|") != "hi |there" {
		t.Errorf("deltas = %v, want them handed over as they arrived", deltas)
	}
	if usage.Prompt != 4 || usage.Completion != 2 {
		t.Errorf("usage = %+v, want 4 prompt and 2 completion", usage)
	}
}

// An opencode model the table puts on /chat/completions takes the ordinary
// path, Anthropic client built but unused.
func TestAnOpenCodeChatModelStaysOnChatCompletions(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "deepseek-v4-flash",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKOpenCode, "k", "deepseek-v4-flash",
		srv.URL+"/zen/go/v1", "", "", 5*time.Second, slog.Default())
	if _, err := client.Complete(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("deepseek-v4-flash"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if path != "/zen/go/v1/chat/completions" {
		t.Errorf("path = %q, want /zen/go/v1/chat/completions", path)
	}
}
