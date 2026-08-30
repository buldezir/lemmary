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
)

// scriptedTurn is one canned model response: either tool calls, or the prose
// the answer phase streams back.
type scriptedTurn struct {
	toolCalls []scriptedToolCall
	content   string
}

type scriptedToolCall struct {
	name string
	args string
}

// researchHarness fakes an OpenAI-compatible endpoint that replays turns in
// order, and records what the agent sent.
type researchHarness struct {
	mu       sync.Mutex
	turns    []scriptedTurn
	next     int
	requests []map[string]any
}

func (h *researchHarness) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)

	h.mu.Lock()
	h.requests = append(h.requests, body)
	turn := scriptedTurn{content: "No further information."}
	if h.next < len(h.turns) {
		turn = h.turns[h.next]
		h.next++
	}
	h.mu.Unlock()

	if stream, _ := body["stream"].(bool); stream {
		writeChatStream(w, turn.content)
		return
	}
	writeToolCallJSON(w, turn)
}

func (h *researchHarness) requestCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.requests)
}

func (h *researchHarness) request(i int) map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.requests[i]
}

func writeToolCallJSON(w http.ResponseWriter, turn scriptedTurn) {
	message := map[string]any{"role": "assistant", "content": turn.content}
	if len(turn.toolCalls) > 0 {
		calls := make([]map[string]any, 0, len(turn.toolCalls))
		for i, call := range turn.toolCalls {
			calls = append(calls, map[string]any{
				"id":       fmt.Sprintf("call_%d", i),
				"type":     "function",
				"function": map[string]any{"name": call.name, "arguments": call.args},
			})
		}
		message["tool_calls"] = calls
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1,
		"model":   "test",
		"choices": []map[string]any{{"index": 0, "message": message, "finish_reason": "stop"}},
	})
}

// writeChatStream emits the answer as SSE chunks, one word at a time, the way
// a real provider does.
func writeChatStream(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	for i, word := range strings.SplitAfter(content, " ") {
		if word == "" {
			continue
		}
		chunk := map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion.chunk",
			"created": 1,
			"model":   "test",
			"choices": []map[string]any{{"index": i, "delta": map[string]any{"content": word}}},
		}
		encoded, _ := json.Marshal(chunk)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
		if flusher != nil {
			flusher.Flush()
		}
	}
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func newResearchAgent(t *testing.T, contextTokens int, turns ...scriptedTurn) (*researchHarness, SearchAgent) {
	t.Helper()
	h := &researchHarness{turns: turns}
	srv := httptest.NewServer(http.HandlerFunc(h.handler))
	t.Cleanup(srv.Close)
	agent := NewSearchAgent("openai", "test-key", "mistral-small-latest", srv.URL, 5*time.Second, "en,de", "en", contextTokens, slog.Default())
	return h, agent
}

func hitsFor(ids ...string) []DocumentHit {
	hits := make([]DocumentHit, 0, len(ids))
	for _, id := range ids {
		hits = append(hits, DocumentHit{ID: id, Title: "Doc " + id, OCRSnippet: "…snippet…"})
	}
	return hits
}

func TestResearchSearchesThenReadsThenAnswers(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t, 128000,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"car insurance"}`}}},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: `{"ids":["doc1","doc2"]}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "You paid 200 EUR, see [Doc doc1](/document/doc1)."},
	)

	var readIDs []string
	var events []ResearchEvent
	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "how much did I pay?"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor("doc1", "doc2"), nil
		},
		Read: func(_ context.Context, ids []string, maxTotalChars int) ([]DocumentContent, error) {
			readIDs = ids
			if maxTotalChars <= 0 {
				t.Fatalf("reader received no budget")
			}
			return []DocumentContent{
				{ID: "doc1", Title: "Doc doc1", Text: "Premium 200 EUR"},
				{ID: "doc2", Title: "Doc doc2", Text: "Premium 0 EUR"},
			}, nil
		},
	}, func(e ResearchEvent) { events = append(events, e) })
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	if len(readIDs) != 2 || readIDs[0] != "doc1" || readIDs[1] != "doc2" {
		t.Fatalf("read ids = %v", readIDs)
	}
	if !strings.Contains(result.Reply, "/document/doc1") {
		t.Fatalf("reply lost its citation: %q", result.Reply)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("documents = %d, want 2", len(result.Documents))
	}

	// Search, read, and answer each report a start and a done.
	var kinds []string
	for _, e := range events {
		if e.Type == "step" {
			kinds = append(kinds, e.Kind+":"+e.Status)
		}
	}
	want := []string{"search:start", "search:done", "read:start", "read:done", "answer:start", "answer:done"}
	if strings.Join(kinds, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v, want %v", kinds, want)
	}

	// The answer streamed, and it streamed with no tools declared so the model
	// cannot emit tool markup into visible prose.
	var streamed strings.Builder
	for _, e := range events {
		if e.Type == "delta" {
			streamed.WriteString(e.Content)
		}
	}
	if !strings.Contains(streamed.String(), "200 EUR") {
		t.Fatalf("answer did not stream, got %q", streamed.String())
	}
	last := h.request(h.requestCount() - 1)
	if _, ok := last["tools"]; ok {
		t.Fatalf("answer phase declared tools: %v", last)
	}
	if stream, _ := last["stream"].(bool); !stream {
		t.Fatalf("answer phase was not streamed: %v", last)
	}
}

func TestResearchIsNotCappedAtFourRounds(t *testing.T) {
	t.Parallel()
	// The removed deep mode stopped after four tool rounds. A research run that
	// keeps finding new documents must not stop there.
	turns := make([]scriptedTurn, 0, 9)
	for i := 0; i < 8; i++ {
		turns = append(turns, scriptedTurn{toolCalls: []scriptedToolCall{
			{name: "search_documents", args: fmt.Sprintf(`{"query":"term %d"}`, i)},
		}})
	}
	turns = append(turns, scriptedTurn{content: "done"})
	_, agent := newResearchAgent(t, 128000, turns...)

	searches := 0
	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "summarise everything"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return hitsFor(fmt.Sprintf("doc%d", searches)), nil
		},
		Read: func(_ context.Context, _ []string, _ int) ([]DocumentContent, error) {
			return nil, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if searches != 8 {
		t.Fatalf("searches = %d, want 8 — the run was cut short", searches)
	}
	if len(result.Documents) != 8 {
		t.Fatalf("documents = %d, want 8", len(result.Documents))
	}
}

func TestResearchStopsWhenContextBudgetIsSpent(t *testing.T) {
	t.Parallel()
	turns := make([]scriptedTurn, 0, 200)
	for i := 0; i < 200; i++ {
		turns = append(turns, scriptedTurn{toolCalls: []scriptedToolCall{
			{name: "search_documents", args: fmt.Sprintf(`{"query":"term %d"}`, i)},
		}})
	}
	// The smallest window the budget accepts, so it runs out quickly.
	_, agent := newResearchAgent(t, 1, turns...)

	searches := 0
	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "summarise everything"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return hitsFor(fmt.Sprintf("doc%d", searches)), nil
		},
		Read: func(_ context.Context, _ []string, _ int) ([]DocumentContent, error) {
			return nil, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if searches >= 200 {
		t.Fatalf("budget did not stop the run: %d searches", searches)
	}
	// Exhausting the budget still has to produce an answer, not an error.
	if strings.TrimSpace(result.Reply) == "" {
		t.Fatal("budget exhaustion produced no answer")
	}
}

func TestResearchSuppressesRepeatedIdenticalCalls(t *testing.T) {
	t.Parallel()
	turns := make([]scriptedTurn, 0, 6)
	for i := 0; i < 5; i++ {
		turns = append(turns, scriptedTurn{toolCalls: []scriptedToolCall{
			{name: "search_documents", args: `{"query":"same"}`},
		}})
	}
	turns = append(turns, scriptedTurn{content: "done"})
	_, agent := newResearchAgent(t, 128000, turns...)

	searches := 0
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "anything"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ []string, _ int) ([]DocumentContent, error) {
			return nil, nil
		},
	}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if searches != 1 {
		t.Fatalf("identical query hit the index %d times, want 1", searches)
	}
}

func TestResearchStopsAfterStalledRounds(t *testing.T) {
	t.Parallel()
	// Distinct queries that surface nothing: the stall detector, not a round
	// cap, is what ends this.
	turns := make([]scriptedTurn, 0, 30)
	for i := 0; i < 30; i++ {
		turns = append(turns, scriptedTurn{toolCalls: []scriptedToolCall{
			{name: "search_documents", args: fmt.Sprintf(`{"query":"nothing %d"}`, i)},
		}})
	}
	_, agent := newResearchAgent(t, 128000, turns...)

	searches := 0
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "anything"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return nil, nil
		},
		Read: func(_ context.Context, _ []string, _ int) ([]DocumentContent, error) {
			return nil, nil
		},
	}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if searches != maxStalledRounds {
		t.Fatalf("searches = %d, want %d", searches, maxStalledRounds)
	}
}

func TestResearchRefusesToReadUnseenDocuments(t *testing.T) {
	t.Parallel()
	_, agent := newResearchAgent(t, 128000,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: `{"ids":["secret"]}`}}},
		scriptedTurn{content: "done"},
	)

	reads := 0
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "read someone else's document"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return nil, nil
		},
		Read: func(_ context.Context, _ []string, _ int) ([]DocumentContent, error) {
			reads++
			return nil, nil
		},
	}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if reads != 0 {
		t.Fatal("agent read an id it never found through search")
	}
}

func TestValidateCitationsUnwrapsInventedDocuments(t *testing.T) {
	t.Parallel()
	seen := map[string]struct{}{"real1": {}}
	got := validateCitations(
		"See [Real invoice](/document/real1) and [Made up](/document/fake9).",
		seen,
	)
	if !strings.Contains(got, "[Real invoice](/document/real1)") {
		t.Fatalf("real citation was dropped: %q", got)
	}
	if strings.Contains(got, "/document/fake9") {
		t.Fatalf("invented citation survived: %q", got)
	}
	if !strings.Contains(got, "Made up") {
		t.Fatalf("invented citation text should remain as plain text: %q", got)
	}
}

func TestDecodeReadArgsAcceptsLooseShapes(t *testing.T) {
	t.Parallel()
	ids, err := decodeReadArgs(`{"ids":["b"," a ","a",""]}`)
	if err != nil {
		t.Fatalf("decodeReadArgs: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Fatalf("ids = %v, want deduped and sorted [a b]", ids)
	}

	// Models reach for a singular id often enough to be worth accepting.
	if ids, err = decodeReadArgs(`{"id":"solo"}`); err != nil || len(ids) != 1 || ids[0] != "solo" {
		t.Fatalf("singular id form = %v err=%v", ids, err)
	}
	if _, err := decodeReadArgs(`{"nope":1}`); err == nil {
		t.Fatal("expected an error when no ids are present")
	}
}

func TestBuildResearchSystemPromptDemandsReadingBeforeClaiming(t *testing.T) {
	t.Parallel()
	prompt := buildResearchSystemPrompt("en,de", "en", []string{"invoice"})
	for _, want := range []string{
		"read_documents",
		"Never state what a document contains without reading it",
		"context_chars_left",
		"invoice",
		"en,de",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("research prompt missing %q: %s", want, prompt)
		}
	}
}
