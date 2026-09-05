package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// reasoningHarness fakes the gpt-5-family behaviour from issue #49: a request
// carrying function tools is refused unless reasoning_effort is explicitly
// "none". Requests without tools are served normally, whatever they say.
type reasoningHarness struct {
	mu         sync.Mutex
	requests   []map[string]any
	turns      []scriptedTurn
	next       int
	rejections int
	// responsesTurns, when set, makes /responses a working endpoint serving
	// these turns. Absent, the provider has no such endpoint and answers 404 --
	// which is the case where reasoning_effort=none is the only way out.
	responsesTurns []scriptedTurn
	responsesNext  int
	responsesTried int
}

func (h *reasoningHarness) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	if strings.HasSuffix(r.URL.Path, "/responses") {
		h.mu.Lock()
		h.responsesTried++
		serve := len(h.responsesTurns) > 0
		turn := scriptedTurn{content: "No further information."}
		if h.responsesNext < len(h.responsesTurns) {
			turn = h.responsesTurns[h.responsesNext]
			h.responsesNext++
		}
		h.mu.Unlock()
		if !serve {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "unknown endpoint"}})
			return
		}
		if stream, _ := body["stream"].(bool); stream {
			writeResponsesStream(w, turn.content)
			return
		}
		writeResponsesJSON(w, turn, "")
		return
	}

	h.mu.Lock()
	h.requests = append(h.requests, body)
	tools, _ := body["tools"].([]any)
	effort, _ := body["reasoning_effort"].(string)
	if len(tools) > 0 && effort != "none" {
		h.rejections++
		h.mu.Unlock()
		writeReasoningEffortConflict(w)
		return
	}
	turn := scriptedTurn{content: "No further information."}
	if h.next < len(h.turns) {
		turn = h.turns[h.next]
		h.next++
	}
	h.mu.Unlock()

	if stream, _ := body["stream"].(bool); stream {
		writeChatStream(w, turn.content, turn.cutOff)
		return
	}
	writeToolCallJSON(w, turn)
}

// writeReasoningEffortConflict reproduces the 400 from issue #49 verbatim.
func writeReasoningEffortConflict(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "Function tools with reasoning_effort are not supported for gpt-5.6-luna in /v1/chat/completions. " +
				"To use function tools, use /v1/responses or set reasoning_effort to 'none'.",
			"type":  "invalid_request_error",
			"param": "reasoning_effort",
			"code":  nil,
		},
	})
}

func (h *reasoningHarness) request(i int) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests[i]
}

func (h *reasoningHarness) requestCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func effortOf(body map[string]any) string {
	effort, _ := body["reasoning_effort"].(string)
	return effort
}

func hasTools(body map[string]any) bool {
	tools, _ := body["tools"].([]any)
	return len(tools) > 0
}

// Issue #49, on a provider with no Responses endpoint to move to: the refusal's
// other option, reasoning_effort=none, is then the only way to keep the tools.
func TestResearchFallsBackToReasoningEffortNoneAndRemembers(t *testing.T) {
	// Not parallel: the learned-model set is process-wide.
	model := "gpt-5.6-luna-research-test"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &reasoningHarness{turns: []scriptedTurn{
		{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"car insurance"}`}}},
		{content: "ready"},
		{content: "You paid 200 EUR, see [Doc doc1](/document/doc1)."},
	}}
	srv := httptest.NewServer(http.HandlerFunc(h.handler))
	t.Cleanup(srv.Close)
	base := srv.URL
	agent := NewSearchAgent("openai", "test-key", model, base, 5*time.Second, "en,de", "en", slog.Default())

	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "how much did I pay?"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
			return []DocumentContent{{ID: "doc1", Title: "Doc doc1", Text: "Premium 200 EUR"}}, nil
		},
	}, func(ResearchEvent) {})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if !strings.Contains(result.Reply, "/document/doc1") {
		t.Fatalf("reply = %q", result.Reply)
	}

	if h.rejections != 1 {
		t.Fatalf("rejections = %d, want exactly 1: the model should be remembered after the first refusal", h.rejections)
	}
	// /responses is the better of the two options the refusal names, so it has
	// to be offered the request before "none" is settled for.
	if h.responsesTried != 1 {
		t.Fatalf("responses attempts = %d, want 1 before falling back to none", h.responsesTried)
	}
	if got := effortOf(h.request(0)); got != "" {
		t.Fatalf("first request already sent reasoning_effort=%q; we should not send it unprompted", got)
	}
	if got := effortOf(h.request(1)); got != "none" {
		t.Fatalf("retry reasoning_effort = %q, want none", got)
	}
	// Every later tool-carrying round must arrive pre-corrected.
	for i := 2; i < h.requestCount(); i++ {
		body := h.request(i)
		if hasTools(body) && effortOf(body) != "none" {
			t.Fatalf("request %d carries tools but reasoning_effort = %q", i, effortOf(body))
		}
	}
	if !needsNoReasoningEffort(base, model) {
		t.Fatal("model was not remembered")
	}
}

// The streamed answer phase sends no tools, so it must keep the model's own
// reasoning rather than inheriting "none" from the tool rounds.
func TestReasoningEffortNoneIsNotAppliedWithoutTools(t *testing.T) {
	model := "gpt-5.6-luna-extract-test"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		writeChatJSON(w, extractTestJSON)
	}))
	t.Cleanup(srv.Close)
	rememberNoReasoningEffort(srv.URL, model)

	client := NewOpenAIClient("openai", "test-key", model, srv.URL, "v1", "", 5*time.Second, slog.Default())
	if _, err := client.ExtractMetadata(context.Background(), "Invoice from Acme", ExtractionCatalog{}); err != nil {
		t.Fatalf("ExtractMetadata: %v", err)
	}
	if strings.Contains(body, "reasoning_effort") {
		t.Fatalf("tool-less request sent reasoning_effort: %s", body)
	}
}

// The refusal names /responses first because it keeps the tools *and* the
// reasoning; "none" keeps the tools by switching the reasoning off. Where both
// are available, the request must take the first.
func TestReasoningEffortConflictPrefersTheResponsesAPI(t *testing.T) {
	model := "gpt-5.6-luna-prefers-responses"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	h := &reasoningHarness{responsesTurns: []scriptedTurn{
		{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"insurance"}`}}},
		{content: "ready"},
		{content: "Answer with [Doc doc1](/document/doc1)."},
	}}
	srv := httptest.NewServer(http.HandlerFunc(h.handler))
	t.Cleanup(srv.Close)
	base := srv.URL
	agent := NewSearchAgent("openai", "test-key", model, base, 5*time.Second, "en,de", "en", slog.Default())

	if _, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "how much did I pay?"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
			return []DocumentContent{{ID: "doc1", Title: "Doc doc1", Text: "Premium 200 EUR"}}, nil
		},
	}, func(ResearchEvent) {}); err != nil {
		t.Fatalf("Research: %v", err)
	}

	if needsNoReasoningEffort(base, model) {
		t.Fatal("the model was pinned to reasoning_effort=none even though /responses served it")
	}
	if !needsResponsesAPI(base, model) {
		t.Fatal("the model was not remembered as a Responses one")
	}
	// One refused chat request, and nothing sent to /chat/completions after it.
	for i, body := range h.requests {
		if effortOf(body) == "none" {
			t.Fatalf("request %d switched the reasoning off: %v", i, body)
		}
	}
	if len(h.requests) != 1 {
		t.Fatalf("chat/completions requests = %d, want 1", len(h.requests))
	}
}

// A "none" the provider also refuses is not worth pinning: the next call would
// send a value already known to fail.
func TestReasoningEffortNoneIsNotRememberedWhenItAlsoFails(t *testing.T) {
	model := "gpt-5.6-luna-none-also-fails"
	resetModelNotes()
	t.Cleanup(resetModelNotes)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/responses") {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "unknown endpoint"}})
			return
		}
		writeReasoningEffortConflict(w)
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient("openai", "test-key", model, srv.URL, "v1", "", 5*time.Second, slog.Default())
	_, err := CompleteChat(context.Background(), client.client, slog.Default(), "openai", srv.URL, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(model),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
		Tools:    []openai.ChatCompletionToolParam{{Function: shared.FunctionDefinitionParam{Name: "search_documents"}}},
	})
	if err == nil {
		t.Fatal("expected the refusal to surface")
	}
	if needsNoReasoningEffort(srv.URL, model) {
		t.Fatal("a value the provider rejected was remembered anyway")
	}
}

func TestIsReasoningEffortToolConflictError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			"param alone",
			&openai.Error{Param: "reasoning_effort", Message: "unsupported"},
			true,
		},
		{
			// OpenAI-compatible proxies routinely leave param empty.
			"message alone",
			&openai.Error{Message: "Function tools with reasoning_effort are not supported for gpt-5.6-luna"},
			true,
		},
		{
			"wrapped",
			errors.New("openai search completion: Function tools with reasoning_effort are not supported"),
			true,
		},
		{"unrelated param", &openai.Error{Param: "temperature", Message: "temperature does not support 0.2"}, false},
		{"unrelated text", errors.New("context length exceeded"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isReasoningEffortToolConflictError(tc.err); got != tc.want {
				t.Fatalf("isReasoningEffortToolConflictError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
