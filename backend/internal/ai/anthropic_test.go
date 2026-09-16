package ai

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"

	"lemmary/backend/internal/aiprovider"
)

func messageReply(w http.ResponseWriter, text string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant",
		"model": "claude-opus-5", "stop_reason": "end_turn",
		"content": []any{map[string]any{"type": "text", "text": text}},
		"usage":   map[string]any{"input_tokens": 9, "output_tokens": 3},
	})
}

// Every model on this SDK is served by the Messages API, whatever its id, and
// the routing table opencode.Endpoint carries has no say in it.
func TestAnAnthropicModelReachesTheMessagesEndpoint(t *testing.T) {
	var path, apiKey, version, session, bearer string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		apiKey = r.Header.Get("x-api-key")
		version = r.Header.Get("anthropic-version")
		session = r.Header.Get(aiprovider.SessionHeader)
		bearer = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&body)
		messageReply(w, "from anthropic")
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKAnthropic, "sk-ant-test", "claude-opus-5",
		srv.URL+"/v1", "", "", 5*time.Second, slog.Default())

	// A session id on the context, to prove it does not leak into a header
	// that only OpenCode routes on.
	ctx := aiprovider.WithSession(context.Background(), "conv123")
	resp, err := client.Complete(ctx, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("claude-opus-5"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Choices[0].Message.Content != "from anthropic" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
	if path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", path)
	}
	if apiKey != "sk-ant-test" {
		t.Errorf("x-api-key = %q, want the provider's key", apiKey)
	}
	if version == "" {
		t.Error("anthropic-version is missing; the API requires it on every request")
	}
	if session != "" {
		t.Errorf("%s = %q, want nothing: it is OpenCode's routing key", aiprovider.SessionHeader, session)
	}
	if bearer != "" {
		t.Errorf("Authorization = %q, want nothing beside x-api-key", bearer)
	}
	if u := usageOf(resp); u.Prompt != 9 || u.Completion != 3 {
		t.Errorf("usage = %+v, want 9 prompt and 3 completion", u)
	}
}

// Claude's own default is high, and these calls are extraction and search over
// documents already in hand, paid for by the token.
func TestAnAnthropicRequestAsksForLowEffortAndNoThinking(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&body)
		messageReply(w, "ok")
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKAnthropic, "k", "claude-opus-5",
		srv.URL+"/v1", "", "", 5*time.Second, slog.Default())
	if _, err := client.Complete(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("claude-opus-5"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	config, _ := body["output_config"].(map[string]any)
	if config == nil || config["effort"] != "low" {
		t.Fatalf("output_config = %v, want effort low", body["output_config"])
	}
	// Thinking off, because a tool loop cannot replay a thinking block through
	// an openai.ChatCompletion and a turn replayed without one is refused.
	thinking, _ := body["thinking"].(map[string]any)
	if thinking == nil || thinking["type"] != "disabled" {
		t.Fatalf("thinking = %v, want type disabled", body["thinking"])
	}

	// A caller that asks for more gets it; the default is only a default.
	if _, err := client.Complete(context.Background(), openai.ChatCompletionNewParams{
		Model:           shared.ChatModel("claude-opus-5"),
		ReasoningEffort: shared.ReasoningEffortHigh,
		Messages:        []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	config, _ = body["output_config"].(map[string]any)
	if config == nil || config["effort"] != "high" {
		t.Fatalf("output_config = %v, want effort high", body["output_config"])
	}
}

// Only Claude 4.5 and later take output_config.effort. A bound older model must
// cost one retry, not every request.
func TestAnAnthropicModelThatRefusesEffortIsRetriedWithoutItAndRemembered(t *testing.T) {
	t.Cleanup(resetModelNotes)
	resetModelNotes()

	var calls atomic.Int32
	var withEffort atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["output_config"]; ok {
			withEffort.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"output_config.effort: unsupported for this model"}}`))
			return
		}
		messageReply(w, "ok")
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKAnthropic, "k", "claude-haiku-4-5",
		srv.URL+"/v1", "", "", 5*time.Second, slog.Default())
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("claude-haiku-4-5"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}

	if _, err := client.Complete(context.Background(), params); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if calls.Load() != 2 || withEffort.Load() != 1 {
		t.Fatalf("calls = %d with %d carrying effort, want one refused and one retried", calls.Load(), withEffort.Load())
	}

	// The second request skips the field outright: the refusal is a property of
	// the model, not of that one call.
	if _, err := client.Complete(context.Background(), params); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if calls.Load() != 3 || withEffort.Load() != 1 {
		t.Fatalf("calls = %d with %d carrying effort, want the refusal remembered", calls.Load(), withEffort.Load())
	}
}

// The streamed path chat runs on, wired the same way.
func TestAnAnthropicModelStreams(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range [][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":4,"output_tokens":0}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi "}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"there"}}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":4,"output_tokens":2}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			_, _ = w.Write([]byte("event: " + event[0] + "\ndata: " + event[1] + "\n\n"))
		}
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKAnthropic, "k", "claude-opus-5",
		srv.URL+"/v1", "", "", 5*time.Second, slog.Default())

	var deltas []string
	text, usage, err := client.completeStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("claude-opus-5"),
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

// refusingServer answers a 400 naming `field` while the request still carries
// it, and succeeds once it is gone. It counts the requests that carried it.
type refusingServer struct {
	field   string
	carries func(map[string]any) bool
	calls   atomic.Int32
	refused atomic.Int32
}

func (s *refusingServer) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if s.carries(body) {
			s.refused.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"` +
				s.field + ` is not supported for this model"}}`))
			return
		}
		messageReply(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// Every production caller sets a temperature, and Claude removed the parameter
// after Opus 4.6 -- so without this the whole SDK fails on its first request.
func TestAnAnthropicModelThatRefusesTemperatureIsRetriedWithoutIt(t *testing.T) {
	t.Cleanup(resetModelNotes)
	resetModelNotes()

	srv := &refusingServer{
		field:   "temperature",
		carries: func(body map[string]any) bool { _, ok := body["temperature"]; return ok },
	}
	base := srv.start(t) + "/v1"

	client := NewOpenAIClient(aiprovider.SDKAnthropic, "k", "claude-opus-5",
		base, "", "", 5*time.Second, slog.Default())
	params := openai.ChatCompletionNewParams{
		Model:       shared.ChatModel("claude-opus-5"),
		Temperature: openai.Float(0.1),
		Messages:    []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}

	if _, err := client.Complete(context.Background(), params); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if srv.calls.Load() != 2 || srv.refused.Load() != 1 {
		t.Fatalf("calls = %d with %d carrying temperature, want one refused and one retried", srv.calls.Load(), srv.refused.Load())
	}

	// A second call skips it outright: the refusal is a property of the model.
	if _, err := client.Complete(context.Background(), params); err != nil {
		t.Fatalf("second Complete: %v", err)
	}
	if srv.calls.Load() != 3 || srv.refused.Load() != 1 {
		t.Fatalf("calls = %d with %d carrying temperature, want the refusal remembered", srv.calls.Load(), srv.refused.Load())
	}
}

// The Fable family refuses to have thinking disabled at all, where every other
// Claude requires it here. One ladder covers both.
func TestAnAnthropicModelThatRefusesDisabledThinkingIsRetriedWithoutIt(t *testing.T) {
	t.Cleanup(resetModelNotes)
	resetModelNotes()

	srv := &refusingServer{
		field:   "thinking",
		carries: func(body map[string]any) bool { _, ok := body["thinking"]; return ok },
	}
	base := srv.start(t) + "/v1"

	client := NewOpenAIClient(aiprovider.SDKAnthropic, "k", "claude-fable-5-1",
		base, "", "", 5*time.Second, slog.Default())
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("claude-fable-5-1"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}

	if _, err := client.Complete(context.Background(), params); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if srv.calls.Load() != 2 || srv.refused.Load() != 1 {
		t.Fatalf("calls = %d with %d carrying thinking, want one refused and one retried", srv.calls.Load(), srv.refused.Load())
	}
}

// A refusal this ladder cannot fix is the caller's to see, not something to
// retry a field lighter until the request is empty.
func TestAnAnthropicRefusalAboutSomethingElseIsNotRetried(t *testing.T) {
	t.Cleanup(resetModelNotes)
	resetModelNotes()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"credit balance is too low"}}`))
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKAnthropic, "k", "claude-opus-5",
		srv.URL+"/v1", "", "", 5*time.Second, slog.Default())
	_, err := client.Complete(context.Background(), openai.ChatCompletionNewParams{
		Model:       shared.ChatModel("claude-opus-5"),
		Temperature: openai.Float(0.1),
		Messages:    []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err == nil {
		t.Fatal("a refusal about the account was swallowed")
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want the one", calls.Load())
	}
	// And nothing was remembered, so the next model's first request still
	// carries the fields.
	if messagesFieldRefused(srv.URL+"/v1", "claude-opus-5", fieldTemperature) {
		t.Error("an unrelated refusal turned temperature off for the rest of the process")
	}
}

// The streamed path walks the same ladder, and may: a refused field is refused
// before the first event.
func TestAnAnthropicStreamRetriesARefusedField(t *testing.T) {
	t.Cleanup(resetModelNotes)
	resetModelNotes()

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if _, ok := body["temperature"]; ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"temperature is not supported"}}`))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range [][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"claude-opus-5","content":[],"usage":{"input_tokens":4,"output_tokens":0}}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ok"}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			_, _ = w.Write([]byte("event: " + event[0] + "\ndata: " + event[1] + "\n\n"))
		}
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient(aiprovider.SDKAnthropic, "k", "claude-opus-5",
		srv.URL+"/v1", "", "", 5*time.Second, slog.Default())
	text, _, err := client.completeStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model:       shared.ChatModel("claude-opus-5"),
		Temperature: openai.Float(0.2),
		Messages:    []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}, nil)
	if err != nil {
		t.Fatalf("completeStreaming: %v", err)
	}
	if text != "ok" || calls.Load() != 2 {
		t.Fatalf("text = %q after %d calls, want it retried without temperature", text, calls.Load())
	}
}
