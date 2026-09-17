package ai

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"

	"lemmary/backend/internal/aiprovider"
)

// bodyCapturingServer answers every completion plainly and keeps what was sent.
func bodyCapturingServer(t *testing.T) (*httptest.Server, *[]map[string]any) {
	t.Helper()
	bodies := &[]map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		*bodies = append(*bodies, body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "test-model",
			"choices": []map[string]any{{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "done"},
			}},
		})
	}))
	t.Cleanup(server.Close)
	return server, bodies
}

func completeOnce(t *testing.T, ctx context.Context, sdk string) map[string]any {
	t.Helper()
	server, bodies := bodyCapturingServer(t)
	client := NewOpenAIClient(sdk, "test-key", "test-model", server.URL, "", "", 5*time.Second, slog.Default())
	if _, err := client.Complete(ctx, openai.ChatCompletionNewParams{
		Model:    "test-model",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("how much did I pay?")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(*bodies) != 1 {
		t.Fatalf("got %d requests, want 1", len(*bodies))
	}
	return (*bodies)[0]
}

// OpenAI finds the shared prefix itself; the key is only how it groups the
// requests that have one. The conversation id is that group.
func TestOpenAICarriesTheConversationAsThePromptCacheKey(t *testing.T) {
	ctx := aiprovider.WithSession(context.Background(), "session-abc")
	body := completeOnce(t, ctx, aiprovider.SDKOpenAI)
	if got, _ := body["prompt_cache_key"].(string); got != "session-abc" {
		t.Fatalf("prompt_cache_key = %q, want the session id", got)
	}
}

// Without a session there is nothing to group by, and an empty key is worse
// than none: it would pool every conversation onto one.
func TestOpenAISendsNoPromptCacheKeyWithoutASession(t *testing.T) {
	body := completeOnce(t, context.Background(), aiprovider.SDKOpenAI)
	if _, ok := body["prompt_cache_key"]; ok {
		t.Fatalf("prompt_cache_key sent with no session: %v", body["prompt_cache_key"])
	}
}

// A provider that has not been taught the field must not be sent it: an unknown
// parameter is a 400, not a missed optimisation.
func TestOtherProvidersGetNoCacheFields(t *testing.T) {
	body := completeOnce(t, aiprovider.WithSession(context.Background(), "session-abc"), aiprovider.SDKMistral)
	if _, ok := body["prompt_cache_key"]; ok {
		t.Fatalf("prompt_cache_key sent to a provider that does not take it")
	}
	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	if content, _ := messages[0].(map[string]any)["content"].(string); content == "" {
		t.Fatalf("the message was rewritten into parts for a provider that wants a string: %v", messages[0])
	}
}

// Anthropic-shaped providers cache only what a breakpoint marks, and OpenRouter
// passes the breakpoint through. One mark at the tail caches everything before
// it: the system prompt, the tools and every turn so far.
func TestOpenRouterMarksTheEndOfTheConversation(t *testing.T) {
	body := completeOnce(t, context.Background(), aiprovider.SDKOpenRouter)

	messages, _ := body["messages"].([]any)
	if len(messages) != 1 {
		t.Fatalf("messages = %v", messages)
	}
	parts, _ := messages[0].(map[string]any)["content"].([]any)
	if len(parts) != 1 {
		t.Fatalf("content = %v, want one text part carrying the breakpoint", messages[0])
	}
	part, _ := parts[0].(map[string]any)
	if part["text"] != "how much did I pay?" {
		t.Fatalf("the breakpoint lost the text: %v", part)
	}
	control, _ := part["cache_control"].(map[string]any)
	if control["type"] != "ephemeral" {
		t.Fatalf("cache_control = %v, want ephemeral", part["cache_control"])
	}
}

// The breakpoint moves to the tail on every request, so it must not be left
// behind on the message the agent loop keeps in its own array: a stale mark in
// the middle changes a message the provider has already cached.
func TestTheBreakpointDoesNotEditTheCallersMessages(t *testing.T) {
	t.Parallel()

	messages := []openai.ChatCompletionMessageParamUnion{
		openai.UserMessage("first"),
		openai.ToolMessage(`{"documents":[]}`, "call_0"),
	}
	marked := withCacheBreakpoint(messages)

	if !messages[1].OfTool.Content.OfString.Valid() {
		t.Fatal("the caller's own tool result was rewritten into parts")
	}
	if len(marked[1].OfTool.Content.OfArrayOfContentParts) != 1 {
		t.Fatalf("the marked copy carries no breakpoint: %+v", marked[1])
	}
	if marked[0].OfUser != messages[0].OfUser {
		t.Fatal("messages before the breakpoint were copied, which changes nothing and costs the cache")
	}
}

// A gpt-5 model refused alongside its tools is moved to /responses and stays
// the same conversation. The key has to cross with it, or the endpoint change
// silently costs the cache it was added to keep.
func TestThePromptCacheKeyCrossesToTheResponsesAPI(t *testing.T) {
	model := "responses-only-cache-key"
	resetModelNotes()
	t.Cleanup(resetModelNotes)
	h := &responsesHarness{respTurns: []scriptedTurn{{content: "ok"}}}
	base := newResponsesHarness(t, h)
	rememberResponsesAPI(base, model)
	client := NewOpenAIClient(aiprovider.SDKOpenAI, "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

	ctx := aiprovider.WithSession(context.Background(), "session-abc")
	if _, err := client.Complete(ctx, openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("how much did I pay?")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got, _ := h.responsesBody(0)["prompt_cache_key"].(string); got != "session-abc" {
		t.Fatalf("prompt_cache_key = %q, want the session id", got)
	}
}
