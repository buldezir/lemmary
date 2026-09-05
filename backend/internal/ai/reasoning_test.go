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
}

func (h *reasoningHarness) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

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

// Issue #49: the research loop used to die on the first round because the model
// defaults reasoning_effort server-side and then refuses the tools we sent.
func TestResearchRetriesOnceWithReasoningEffortNoneThenRemembers(t *testing.T) {
	// Not parallel: the learned-model set is process-wide.
	model := "gpt-5.6-luna-research-test"
	resetNoReasoningEffort()
	t.Cleanup(resetNoReasoningEffort)

	h := &reasoningHarness{turns: []scriptedTurn{
		{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"car insurance"}`}}},
		{content: "ready"},
		{content: "You paid 200 EUR, see [Doc doc1](/document/doc1)."},
	}}
	srv := httptest.NewServer(http.HandlerFunc(h.handler))
	t.Cleanup(srv.Close)
	agent := NewSearchAgent("openai", "test-key", model, srv.URL, 5*time.Second, "en,de", "en", slog.Default())

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
	if !needsNoReasoningEffort(model) {
		t.Fatal("model was not remembered")
	}
}

// The streamed answer phase sends no tools, so it must keep the model's own
// reasoning rather than inheriting "none" from the tool rounds.
func TestReasoningEffortNoneIsNotAppliedWithoutTools(t *testing.T) {
	model := "gpt-5.6-luna-extract-test"
	resetNoReasoningEffort()
	t.Cleanup(resetNoReasoningEffort)
	rememberNoReasoningEffort(model)

	body := captureChatBody(t, model)
	if strings.Contains(body, "reasoning_effort") {
		t.Fatalf("tool-less request sent reasoning_effort: %s", body)
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
