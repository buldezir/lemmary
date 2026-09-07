package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// responsesHarness fakes a provider that serves some models only on
// /v1/responses, the way OpenCode Zen does for gpt-5.6-luna: /chat/completions
// answers with a bare 500 whatever you send it.
type responsesHarness struct {
	mu sync.Mutex
	// chatStatus is what /chat/completions returns; 0 means serve normally.
	chatStatus    int
	chatBodies    []map[string]any
	respBodies    []map[string]any
	respTurns     []scriptedTurn
	respNext      int
	responsesGone bool
	// respStatus, when set, is the status the Response body reports -- the
	// Responses API says a generation failed with a 200 and a status field.
	respStatus string
	// streamError makes the /responses stream end with an error event.
	streamError bool
}

func (h *responsesHarness) handle(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	if strings.HasSuffix(r.URL.Path, "/responses") {
		h.mu.Lock()
		h.respBodies = append(h.respBodies, body)
		gone := h.responsesGone
		turn := scriptedTurn{content: "No further information."}
		if h.respNext < len(h.respTurns) {
			turn = h.respTurns[h.respNext]
			h.respNext++
		}
		h.mu.Unlock()
		if gone {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "no such endpoint"}})
			return
		}
		if stream, _ := body["stream"].(bool); stream {
			if h.streamError {
				writeResponsesStreamError(w)
				return
			}
			writeResponsesStream(w, turn.content)
			return
		}
		writeResponsesJSON(w, turn, h.respStatus)
		return
	}

	h.mu.Lock()
	h.chatBodies = append(h.chatBodies, body)
	status := h.chatStatus
	h.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "Internal server error"}})
		return
	}
	writeChatJSON(w, "served by chat completions")
}

func (h *responsesHarness) counts() (chat int, resp int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.chatBodies), len(h.respBodies)
}

func (h *responsesHarness) responsesBody(i int) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.respBodies[i]
}

// writeResponsesJSON renders a scripted turn in the Responses output shape.
func writeResponsesJSON(w http.ResponseWriter, turn scriptedTurn, status string) {
	output := []map[string]any{}
	// A reasoning item the caller must skip over rather than mistake for text.
	output = append(output, map[string]any{
		"id": "rs_1", "type": "reasoning", "summary": []any{}, "encrypted_content": "opaque",
	})
	if len(turn.toolCalls) > 0 {
		for i, call := range turn.toolCalls {
			output = append(output, map[string]any{
				"id":        fmt.Sprintf("fc_%d", i),
				"type":      "function_call",
				"status":    "completed",
				"call_id":   fmt.Sprintf("call_%d", i),
				"name":      call.name,
				"arguments": call.args,
			})
		}
	}
	if turn.content != "" {
		output = append(output, map[string]any{
			"id": "msg_1", "type": "message", "status": "completed", "role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": turn.content, "annotations": []any{}}},
		})
	}
	if status == "" {
		status = "completed"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "resp_test", "object": "response", "created_at": 1, "status": status,
		"error": map[string]any{"code": "server_error", "message": "the model gave up"},
		"model": "test-model", "output": output,
		"usage": map[string]any{
			"input_tokens":          11,
			"input_tokens_details":  map[string]any{"cached_tokens": 4},
			"output_tokens":         5,
			"output_tokens_details": map[string]any{"reasoning_tokens": 2},
			"total_tokens":          16,
		},
	})
}

func writeResponsesStream(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	seq := 0
	frame := func(payload map[string]any) {
		seq++
		payload["sequence_number"] = seq
		encoded, _ := json.Marshal(payload)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
		if flusher != nil {
			flusher.Flush()
		}
	}
	for _, word := range strings.SplitAfter(content, " ") {
		if word == "" {
			continue
		}
		frame(map[string]any{"type": "response.output_text.delta", "delta": word})
	}
	frame(map[string]any{
		"type": "response.completed",
		"response": map[string]any{
			"id": "resp_stream", "object": "response", "created_at": 1, "status": "completed",
			"model": "test-model", "output": []any{},
			"usage": map[string]any{
				"input_tokens": 7, "input_tokens_details": map[string]any{"cached_tokens": 3},
				"output_tokens": 2, "output_tokens_details": map[string]any{"reasoning_tokens": 0},
				"total_tokens": 9,
			},
		},
	})
}

// writeResponsesStreamError ends a stream the way the Responses API reports an
// in-band failure: a top-level code and message, with no nested "error" object
// for the SDK's decoder to notice.
func writeResponsesStreamError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	flusher, _ := w.(http.Flusher)
	_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"half \",\"sequence_number\":1}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"upstream exploded\",\"param\":null,\"sequence_number\":2}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func newResponsesHarness(t *testing.T, h *responsesHarness) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h.handle))
	t.Cleanup(srv.Close)
	return srv.URL
}

// A model served only on /responses must be discovered once and then addressed
// there directly, for tool rounds and the streamed answer alike.
func TestResearchFallsBackToResponsesAndRemembers(t *testing.T) {
	model := "responses-only-research"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &responsesHarness{chatStatus: http.StatusInternalServerError, respTurns: []scriptedTurn{
		{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"car insurance"}`}}},
		{content: "ready"},
		{content: "You paid 200 EUR, see [Doc doc1](/document/doc1)."},
	}}
	base := newResponsesHarness(t, h)
	agent := NewSearchAgent("openai", "test-key", model, base, 5*time.Second, "en,de", "en", slog.Default())

	var searched bool
	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "how much did I pay?"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searched = true
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
			return []DocumentContent{{ID: "doc1", Title: "Doc doc1", Text: "Premium 200 EUR"}}, nil
		},
	}, func(ResearchEvent) {})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if !searched {
		t.Fatal("the translated function_call never reached the tool")
	}
	if !strings.Contains(result.Reply, "/document/doc1") {
		t.Fatalf("reply = %q", result.Reply)
	}

	chat, resp := h.counts()
	// Two, not one. A 500 does not say whether the endpoint refuses this model
	// or is simply having a bad minute, so the first one is acted on but not
	// believed; the second makes it a pattern and the model is rerouted for
	// good. Both requests still succeed, via the fallback.
	if chat != 2 {
		t.Fatalf("chat/completions attempts = %d, want 2 before an ambiguous refusal is believed", chat)
	}
	if resp < 3 {
		t.Fatalf("responses requests = %d, want the tool rounds and the answer", resp)
	}
	if !needsResponsesAPI(base, model) {
		t.Fatal("model was not remembered")
	}
}

// The translation is the whole feature: a request that leaves as chat
// completions has to arrive as an equivalent Responses call.
func TestResponsesRequestShape(t *testing.T) {
	model := "responses-only-shape"
	resetModelNotes()
	t.Cleanup(resetModelNotes)
	h := &responsesHarness{respTurns: []scriptedTurn{{content: "ok"}}}
	base := newResponsesHarness(t, h)
	rememberResponsesAPI(base, model)
	client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

	params := openai.ChatCompletionNewParams{
		Model: shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("you are an archivist"),
			openai.UserMessage("find my insurance"),
			{OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{{
					ID: "call_7",
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name: "search_documents", Arguments: `{"query":"insurance"}`,
					},
				}},
			}},
			openai.ToolMessage("1 hit", "call_7"),
		},
		Temperature: openai.Float(0.2),
		Tools: []openai.ChatCompletionToolParam{{
			Function: shared.FunctionDefinitionParam{
				Name:        "search_documents",
				Description: openai.String("Search the archive"),
				Parameters:  shared.FunctionParameters{"type": "object"},
			},
		}},
		ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String("auto")},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
	}
	if _, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", base, params); err != nil {
		t.Fatalf("CompleteChat: %v", err)
	}
	if chat, _ := h.counts(); chat != 0 {
		t.Fatalf("a remembered model still tried chat/completions %d times", chat)
	}

	body := h.responsesBody(0)
	if body["model"] != model {
		t.Fatalf("model = %v", body["model"])
	}
	if store, ok := body["store"].(bool); !ok || store {
		t.Fatalf("store = %v, want false: the archive keeps its own conversation state", body["store"])
	}
	if body["temperature"] != 0.2 {
		t.Fatalf("temperature = %v", body["temperature"])
	}
	if body["tool_choice"] != "auto" {
		t.Fatalf("tool_choice = %v", body["tool_choice"])
	}

	text, _ := body["text"].(map[string]any)
	format, _ := text["format"].(map[string]any)
	if format["type"] != "json_object" {
		t.Fatalf("text.format = %v, want json_object", text["format"])
	}

	// Responses tools are flat: no nested "function" object.
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v", tools)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["type"] != "function" || tool["name"] != "search_documents" || tool["description"] != "Search the archive" {
		t.Fatalf("tool = %v", tool)
	}

	input, _ := body["input"].([]any)
	if len(input) != 4 {
		t.Fatalf("input items = %d, want 4: %v", len(input), input)
	}
	want := []struct{ typ, role string }{
		{"", "system"},
		{"", "user"},
		{"function_call", ""},
		{"function_call_output", ""},
	}
	for i, w := range want {
		item, _ := input[i].(map[string]any)
		if w.role != "" && item["role"] != w.role {
			t.Fatalf("input[%d].role = %v, want %v", i, item["role"], w.role)
		}
		if w.typ != "" && item["type"] != w.typ {
			t.Fatalf("input[%d].type = %v, want %v", i, item["type"], w.typ)
		}
	}
	// The call id has to survive, or the model cannot match answer to question.
	call, _ := input[2].(map[string]any)
	if call["call_id"] != "call_7" || call["name"] != "search_documents" {
		t.Fatalf("function_call = %v", call)
	}
	out, _ := input[3].(map[string]any)
	if out["call_id"] != "call_7" || out["output"] != "1 hit" {
		t.Fatalf("function_call_output = %v", out)
	}
}

func TestChatCompletionFromResponse(t *testing.T) {
	t.Parallel()
	var resp responses.Response
	raw := `{"id":"resp_1","model":"m","status":"completed","output":[
	  {"id":"rs_1","type":"reasoning","encrypted_content":"opaque"},
	  {"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello "},{"type":"output_text","text":"world"}]},
	  {"id":"fc_1","type":"function_call","call_id":"call_9","name":"search_documents","arguments":"{\"query\":\"x\"}"}],
	  "usage":{"input_tokens":11,"input_tokens_details":{"cached_tokens":4},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":2},"total_tokens":16}}`
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := chatCompletionFrom(&resp)
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %d", len(got.Choices))
	}
	msg := got.Choices[0].Message
	if msg.Content != "hello world" {
		t.Fatalf("content = %q; reasoning items must be skipped and text parts joined", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "call_9" {
		t.Fatalf("tool calls = %+v; call_id is the id chat completions uses", msg.ToolCalls)
	}
	if msg.ToolCalls[0].Function.Name != "search_documents" || msg.ToolCalls[0].Function.Arguments != `{"query":"x"}` {
		t.Fatalf("tool call function = %+v", msg.ToolCalls[0].Function)
	}
	if got.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason = %q", got.Choices[0].FinishReason)
	}
	if u := usageOf(got); u.Prompt != 11 || u.Completion != 5 || u.Cached != 4 {
		t.Fatalf("usage = %+v", u)
	}
}

func TestStreamingFallsBackToResponses(t *testing.T) {
	model := "responses-only-stream"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &responsesHarness{chatStatus: http.StatusInternalServerError, respTurns: []scriptedTurn{
		{content: "streamed from responses"},
	}}
	base := newResponsesHarness(t, h)
	client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

	var deltas []string
	text, usage, err := client.completeStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("completeStreaming: %v", err)
	}
	if text != "streamed from responses" {
		t.Fatalf("text = %q", text)
	}
	if len(deltas) < 2 {
		t.Fatalf("deltas = %v, want the text to arrive incrementally", deltas)
	}
	if usage.Prompt != 7 || usage.Completion != 2 || usage.Cached != 3 {
		t.Fatalf("usage = %+v, want the totals from response.completed", usage)
	}
	// One ambiguous 500 is acted on but not believed.
	if needsResponsesAPI(base, model) {
		t.Fatal("a single 500 rerouted the model for the rest of the process")
	}
	h.respTurns = append(h.respTurns, scriptedTurn{content: "again"})
	h.respNext = 0
	if _, _, err := client.completeStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}, nil); err != nil {
		t.Fatalf("second completeStreaming: %v", err)
	}
	if !needsResponsesAPI(base, model) {
		t.Fatal("a second refusal should have settled it")
	}
}

// A 500 from an endpoint that has already served this model is the provider
// having a bad minute, not a model that lives elsewhere. The SDK is configured
// with no retries of its own, so without this one hiccup would be enough to
// reroute a model permanently.
func TestAProvenModelIsNotReroutedByATransient500(t *testing.T) {
	model := "dual-api-model"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &responsesHarness{respTurns: []scriptedTurn{{content: "from responses"}}}
	base := newResponsesHarness(t, h)
	client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())
	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}

	// It works here first.
	if _, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", base, params); err != nil {
		t.Fatalf("first CompleteChat: %v", err)
	}
	// Then the provider has a bad minute.
	h.mu.Lock()
	h.chatStatus = http.StatusInternalServerError
	h.mu.Unlock()
	if _, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", base, params); err == nil {
		t.Fatal("expected the 500 to surface rather than being papered over")
	}
	if _, resp := h.counts(); resp != 0 {
		t.Fatalf("a proven model was sent to /responses %d times", resp)
	}
	if needsResponsesAPI(base, model) {
		t.Fatal("a proven model was rerouted by one 500")
	}
}

// 401 and 403 are what OpenCode Zen returns for grok-4.6 and the muse-spark
// models. Unlike a 500 they are not something a working endpoint says about a
// model it serves, so one is enough.
func TestCertainMismatchIsBelievedImmediately(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			model := fmt.Sprintf("certain-%d", status)
			resetModelNotes()
			t.Cleanup(resetModelNotes)

			h := &responsesHarness{chatStatus: status, respTurns: []scriptedTurn{{content: "from responses"}}}
			base := newResponsesHarness(t, h)
			client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

			resp, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", base, openai.ChatCompletionNewParams{
				Model:    shared.ChatModel(model),
				Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
			})
			if err != nil {
				t.Fatalf("CompleteChat: %v", err)
			}
			if resp.Choices[0].Message.Content != "from responses" {
				t.Fatalf("content = %q", resp.Choices[0].Message.Content)
			}
			if !needsResponsesAPI(base, model) {
				t.Fatal("an unambiguous mismatch should be believed the first time")
			}
		})
	}
}

// The Responses API reports a generation it gave up on in the body, with a 200.
// Handed back as a completion it would reach the caller as a successful empty
// answer -- and would count as proof that the endpoint works.
func TestFailedResponseIsNotASuccess(t *testing.T) {
	model := "failed-status-model"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &responsesHarness{chatStatus: http.StatusNotFound, respStatus: "failed"}
	base := newResponsesHarness(t, h)
	client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

	_, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", base, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err == nil {
		t.Fatal("a failed generation was returned as a successful empty answer")
	}
	if needsResponsesAPI(base, model) {
		t.Fatal("a failed generation counted as proof the endpoint serves this model")
	}
}

// A Responses error event puts its code and message at the top level, with no
// nested "error" object, so the SDK's stream decoder does not raise it. Unread,
// an exploded generation looks like a short but successful answer.
func TestStreamErrorEventIsNotSilentlySwallowed(t *testing.T) {
	model := "stream-error-model"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &responsesHarness{streamError: true}
	base := newResponsesHarness(t, h)
	rememberResponsesAPI(base, model)
	client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

	text, _, err := client.completeStreaming(context.Background(), openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}, nil)
	if err == nil {
		t.Fatalf("the stream failed but came back as a success with %q", text)
	}
	if !strings.Contains(err.Error(), "upstream exploded") {
		t.Fatalf("error = %v, want the provider's message", err)
	}
}

// A provider having a bad five minutes is not a provider telling us to use a
// different endpoint.
func TestTransientErrorDoesNotFallBackToResponses(t *testing.T) {
	model := "transient-model"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &responsesHarness{chatStatus: http.StatusServiceUnavailable}
	base := newResponsesHarness(t, h)
	client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

	_, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", base, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err == nil {
		t.Fatal("expected the 503 to surface")
	}
	if _, resp := h.counts(); resp != 0 {
		t.Fatalf("a 503 triggered %d Responses attempts", resp)
	}
	if needsResponsesAPI(base, model) {
		t.Fatal("a transient failure must not reroute the model")
	}
}

// When the fallback was the wrong guess, the caller needs to hear about the
// original failure rather than a 404 from an endpoint the provider never had.
func TestFallbackKeepsTheOriginalError(t *testing.T) {
	model := "no-responses-endpoint"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &responsesHarness{chatStatus: http.StatusInternalServerError, responsesGone: true}
	base := newResponsesHarness(t, h)
	client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

	_, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", base, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error = %v, want the original chat/completions failure", err)
	}
	if needsResponsesAPI(base, model) {
		t.Fatal("a failed fallback must not be remembered")
	}
}

// Models the provider serves normally must never be rerouted.
func TestOrdinaryModelStaysOnChatCompletions(t *testing.T) {
	model := "ordinary-model"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &responsesHarness{}
	base := newResponsesHarness(t, h)
	client := NewOpenAIClient("openai", "test-key", model, base, "v1", "", 5*time.Second, slog.Default())

	resp, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", base, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("CompleteChat: %v", err)
	}
	if resp.Choices[0].Message.Content != "served by chat completions" {
		t.Fatalf("content = %q", resp.Choices[0].Message.Content)
	}
	if _, r := h.counts(); r != 0 {
		t.Fatalf("responses requests = %d, want 0", r)
	}
}
