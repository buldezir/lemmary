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

	"github.com/openai/openai-go/shared"
)

// scriptedTurn is one canned model response: either tool calls, or the prose
// the answer phase streams back.
type scriptedTurn struct {
	toolCalls []scriptedToolCall
	content   string
	// cutOff drops the connection part-way through the streamed answer, the way
	// a request timeout does once the model has already started talking.
	cutOff bool
	// httpStatus, when set, is returned instead of a completion - the way a
	// provider refuses a request that has outgrown the model's context window.
	httpStatus int
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
		writeChatStream(w, turn.content, turn.cutOff)
		return
	}
	if turn.httpStatus != 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(turn.httpStatus)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "This model's maximum context length has been exceeded.",
				"type":    "invalid_request_error",
				"code":    "context_length_exceeded",
			},
		})
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
// a real provider does. cutOff stops half way and drops the connection instead
// of finishing, which is what a timeout mid-generation looks like to the client.
func writeChatStream(w http.ResponseWriter, content string, cutOff bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	words := strings.SplitAfter(content, " ")
	for i, word := range words {
		if word == "" {
			continue
		}
		if cutOff && i >= len(words)/2 {
			// Abort without the terminating frame: the client sees a broken
			// stream, with everything before this already delivered.
			panic(http.ErrAbortHandler)
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

func newResearchAgent(t *testing.T, turns ...scriptedTurn) (*researchHarness, SearchAgent) {
	t.Helper()
	h := &researchHarness{turns: turns}
	srv := httptest.NewServer(http.HandlerFunc(h.handler))
	t.Cleanup(srv.Close)
	agent := NewSearchAgent("openai", "test-key", "mistral-small-latest", srv.URL, 5*time.Second, "en,de", "en", slog.Default())
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
	h, agent := newResearchAgent(t,
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
		Read: func(_ context.Context, req ReadRequest) ([]DocumentContent, error) {
			readIDs = req.IDs
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
	_, agent := newResearchAgent(t, turns...)

	searches := 0
	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "summarise everything"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return hitsFor(fmt.Sprintf("doc%d", searches)), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
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

func TestResearchReturnsAProviderContextError(t *testing.T) {
	t.Parallel()
	// A run that outgrows the model is the provider's to refuse. We used to
	// guess a window and stop gathering before that happened; now the error
	// surfaces instead of a synthesized answer.
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"everything"}`}}},
		scriptedTurn{httpStatus: http.StatusBadRequest},
	)

	searches := 0
	_, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "summarise everything"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
			return nil, nil
		},
	}, nil)
	if err == nil {
		t.Fatal("expected the provider's context-length error")
	}
	if searches != 1 {
		t.Fatalf("searches = %d, want 1 before the provider refused", searches)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "context") && !strings.Contains(err.Error(), "400") {
		t.Fatalf("error should name the provider refusal, got %v", err)
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
	_, agent := newResearchAgent(t, turns...)

	searches := 0
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "anything"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
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
	_, agent := newResearchAgent(t, turns...)

	searches := 0
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "anything"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return nil, nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
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
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: `{"ids":["secret"]}`}}},
		scriptedTurn{content: "done"},
	)

	reads := 0
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "read someone else's document"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return nil, nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
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
	args, err := decodeReadArgs(`{"ids":["b"," a ","a",""]}`)
	if err != nil {
		t.Fatalf("decodeReadArgs: %v", err)
	}
	if len(args.IDs) != 2 || args.IDs[0] != "a" || args.IDs[1] != "b" {
		t.Fatalf("ids = %v, want deduped and sorted [a b]", args.IDs)
	}

	// Models reach for a singular id often enough to be worth accepting.
	if args, err = decodeReadArgs(`{"id":"solo"}`); err != nil || len(args.IDs) != 1 || args.IDs[0] != "solo" {
		t.Fatalf("singular id form = %v err=%v", args.IDs, err)
	}
	if _, err := decodeReadArgs(`{"nope":1}`); err == nil {
		t.Fatal("expected an error when no ids are present")
	}

	// focus rides along on both shapes.
	args, err = decodeReadArgs(`{"ids":["a"],"focus":"total due"}`)
	if err != nil || args.Focus != "total due" {
		t.Fatalf("focus = %+v err=%v", args, err)
	}
	args, err = decodeReadArgs(`{"id":"a","focus":"total due"}`)
	if err != nil || args.Focus != "total due" {
		t.Fatalf("focus on the singular form = %+v err=%v", args, err)
	}
}

func TestBuildResearchSystemPromptDemandsReadingBeforeClaiming(t *testing.T) {
	t.Parallel()
	prompt := buildResearchSystemPrompt("en,de", "en", []string{"invoice"}, false)
	for _, want := range []string{
		"read_documents",
		"Never state what a document contains without reading it",
		"invoice",
		"en,de",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("research prompt missing %q: %s", want, prompt)
		}
	}
}

// TestResearchMarksACutOffAnswerIncomplete covers the failure that looks most
// like success: the answer stream dies part-way through, tokens have already
// reached the user, and the text kept is a fragment. Keeping it is right;
// presenting it as the whole answer is not.
func TestResearchMarksACutOffAnswerIncomplete(t *testing.T) {
	t.Parallel()
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"car insurance"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "You paid 200 EUR in total, and the rest of this answer never arrives.", cutOff: true},
	)

	var events []ResearchEvent
	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "how much did I pay?"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
			return nil, nil
		},
	}, func(e ResearchEvent) { events = append(events, e) })
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	if !result.Incomplete {
		t.Fatalf("a cut-off answer was reported as complete: %q", result.Reply)
	}
	// The partial text is kept: the user watched it arrive, and replacing it
	// with nothing would be worse than saying it is unfinished.
	if !strings.Contains(result.Reply, "You paid") {
		t.Fatalf("partial answer was discarded: %q", result.Reply)
	}
	var streamed strings.Builder
	for _, e := range events {
		if e.Type == "delta" {
			streamed.WriteString(e.Content)
		}
	}
	if streamed.Len() == 0 {
		t.Fatal("no deltas reached the client before the cut")
	}
}

// TestResearchAnswerCompletesNormally is the control for the test above: the
// same path with an intact stream must not be flagged.
func TestResearchAnswerCompletesNormally(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"car insurance"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "You paid 200 EUR in total."},
	)

	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "how much did I pay?"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
			return nil, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if result.Incomplete {
		t.Fatalf("a complete answer was flagged incomplete: %q", result.Reply)
	}

	// Nothing caps the answer any more: what the model may spend on it is the
	// provider's business, not a reserve computed from a guessed window.
	last := h.request(h.requestCount() - 1)
	if _, ok := last["max_tokens"]; ok {
		t.Fatalf("answer phase declared max_tokens: %v", last)
	}
}

func TestEncodeSearchResultsKeepsEveryDocument(t *testing.T) {
	t.Parallel()
	hits := hitsFor("doc1", "doc2", "doc3", "doc4", "doc5")
	content, err := encodeSearchResults(hits)
	if err != nil {
		t.Fatalf("encodeSearchResults: %v", err)
	}
	decoded := assertValidJSON(t, content)
	docs, ok := decoded["documents"].([]any)
	if !ok || len(docs) != len(hits) {
		t.Fatalf("documents = %v, want all %d hits", decoded["documents"], len(hits))
	}
	if count, _ := decoded["count"].(float64); int(count) != len(hits) {
		t.Fatalf("count = %v, want %d", decoded["count"], len(hits))
	}
	// A run is limited only by what the provider accepts, so no envelope field
	// may claim otherwise.
	if _, ok := decoded["context_chars_left"]; ok {
		t.Fatalf("the result still reports a context budget: %s", content)
	}
}

func assertValidJSON(t *testing.T, content string) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		t.Fatalf("tool result is not valid JSON (%v): %s", err, content)
	}
	return decoded
}

func hitsWithPassages(n int, passageRunes int) []DocumentHit {
	hits := make([]DocumentHit, 0, n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("doc%d", i)
		hit := DocumentHit{
			ID:         id,
			Title:      "Doc " + id,
			Summary:    strings.Repeat("s", 200),
			OCRSnippet: "…snippet…",
		}
		for p := 0; p < 3; p++ {
			hit.Passages = append(hit.Passages, Passage{Text: strings.Repeat("p", passageRunes)})
		}
		hits = append(hits, hit)
	}
	return hits
}

// TestEncodeSearchResultsDropsTheSnippetBesidePassages pins the one thing the
// encoder still decides: ocr_snippet is the first passage shortened, so sending
// both spends the conversation twice on the same sentence.
func TestEncodeSearchResultsDropsTheSnippetBesidePassages(t *testing.T) {
	t.Parallel()
	hits := hitsWithPassages(4, 400)

	decoded := assertValidJSON(t, mustEncode(t, hits))
	docs, _ := decoded["documents"].([]any)
	if len(docs) != 4 {
		t.Fatalf("documents = %d, want every hit", len(docs))
	}
	first, _ := docs[0].(map[string]any)
	if passages, _ := first["passages"].([]any); len(passages) != 3 {
		t.Fatalf("every passage should survive, got %d", len(passages))
	}
	if _, ok := first["ocr_snippet"]; ok {
		t.Fatalf("ocr_snippet was paid for alongside passages: %v", first)
	}
	if summary, _ := first["summary"].(string); summary == "" {
		t.Fatalf("the summary should survive: %v", first)
	}

	// With no passages the snippet is all the evidence there is, so it stays.
	bare := hitsFor("doc1")
	bare[0].OCRSnippet = "…snippet…"
	only := assertValidJSON(t, mustEncode(t, bare))
	item, _ := only["documents"].([]any)[0].(map[string]any)
	if snippet, _ := item["ocr_snippet"].(string); snippet != "…snippet…" {
		t.Fatalf("a hit with no passages lost its snippet: %v", item)
	}
}

func mustEncode(t *testing.T, hits []DocumentHit) string {
	t.Helper()
	content, err := encodeSearchResults(hits)
	if err != nil {
		t.Fatalf("encodeSearchResults: %v", err)
	}
	return content
}

// TestResearchReadsDocumentsCitedEarlierWithoutSearching is the follow-up
// question: "and what does the second one say about the deductible?" used to
// start with an empty seen-id set, so the model had to invent a query that
// would rediscover a document it had already read.
func TestResearchReadsDocumentsCitedEarlierWithoutSearching(t *testing.T) {
	t.Parallel()
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: `{"ids":["prior1"],"focus":"deductible"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "The deductible is 300 EUR, see [Prior policy](/document/prior1)."},
	)

	searches := 0
	var gotRequest ReadRequest
	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages:       []ChatMessage{{Role: "user", Content: "what is the deductible?"}},
		PriorDocuments: []DocumentHit{{ID: "prior1", Title: "Prior policy", Passages: []Passage{{Text: "stale"}}}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			searches++
			return nil, nil
		},
		Read: func(_ context.Context, req ReadRequest) ([]DocumentContent, error) {
			gotRequest = req
			return []DocumentContent{{ID: "prior1", Title: "Prior policy", Text: "Deductible 300 EUR"}}, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if searches != 0 {
		t.Fatalf("a document already seen this conversation should be readable without searching, got %d searches", searches)
	}
	if len(gotRequest.IDs) != 1 || gotRequest.IDs[0] != "prior1" {
		t.Fatalf("read ids = %v", gotRequest.IDs)
	}
	if gotRequest.Focus != "deductible" {
		t.Fatalf("focus = %q", gotRequest.Focus)
	}
	if !strings.Contains(result.Reply, "/document/prior1") {
		t.Fatalf("citation to a prior document was unwrapped: %q", result.Reply)
	}
	// Cited, so it joins this turn's results -- the link has to resolve to a card.
	if len(result.Documents) != 1 || result.Documents[0].ID != "prior1" {
		t.Fatalf("documents = %#v", result.Documents)
	}
	if len(result.Documents[0].Passages) != 0 {
		t.Fatalf("passages selected for an earlier question were carried forward: %#v", result.Documents[0].Passages)
	}
}

// TestResearchDoesNotListUncitedPriorDocuments is the other half: carried
// evidence is readable, but it is not a result of this turn until the answer
// says it is.
func TestResearchDoesNotListUncitedPriorDocuments(t *testing.T) {
	t.Parallel()
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"lease"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "The rent is 900 EUR, see [Doc doc1](/document/doc1)."},
	)

	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages:       []ChatMessage{{Role: "user", Content: "what is the rent?"}},
		PriorDocuments: []DocumentHit{{ID: "prior1", Title: "Prior policy"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, _ ReadRequest) ([]DocumentContent, error) {
			return nil, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(result.Documents) != 1 || result.Documents[0].ID != "doc1" {
		t.Fatalf("an uncited prior document was listed as a result: %#v", result.Documents)
	}
}

// TestResearchRereadsWithANewFocus covers the other half of a long document:
// re-reading the same ids is normally suppressed as a repeat, but asking a
// different question of them selects different passages and has to count as
// progress.
func TestResearchRereadsWithANewFocus(t *testing.T) {
	t.Parallel()
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"lease"}`}}},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: `{"ids":["doc1"],"focus":"rent"}`}}},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: `{"ids":["doc1"],"focus":"notice period"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "done"},
	)

	var focuses []string
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "summarise the lease"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor("doc1"), nil
		},
		Read: func(_ context.Context, req ReadRequest) ([]DocumentContent, error) {
			focuses = append(focuses, req.Focus)
			return []DocumentContent{{
				ID:              "doc1",
				Title:           "Doc doc1",
				Text:            "… " + req.Focus + " …",
				Excerpted:       true,
				PassagesOmitted: 2,
			}}, nil
		},
	}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(focuses) != 2 || focuses[0] != "rent" || focuses[1] != "notice period" {
		t.Fatalf("focuses = %v, want both reads to reach the reader", focuses)
	}
}

func TestResearchPromptExplainsFocus(t *testing.T) {
	t.Parallel()
	prompt := buildResearchSystemPrompt("en,de", "en", []string{"invoice"}, false)
	for _, want := range []string{
		"Pass focus to steer the excerpt",
		"survey_documents once",
		"count_documents with the filters",
		"cited earlier in this conversation can be read by id",
		"verbatim passages",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("research prompt missing %q: %s", want, prompt)
		}
	}
	// Nothing in the prompt may point the model at a budget it no longer has.
	for _, gone := range []string{"context_chars_left", "next_offset", "truncated"} {
		if strings.Contains(prompt, gone) {
			t.Fatalf("research prompt still mentions %q: %s", gone, prompt)
		}
	}

	tools := researchTools()
	var read *shared.FunctionDefinitionParam
	for i := range tools {
		if tools[i].Function.Name == "read_documents" {
			read = &tools[i].Function
		}
	}
	if read == nil {
		t.Fatal("read_documents is not declared")
	}
	props, _ := read.Parameters["properties"].(map[string]any)
	for _, want := range []string{"ids", "focus"} {
		if _, ok := props[want]; !ok {
			t.Fatalf("read_documents schema is missing %q: %v", want, props)
		}
	}
	if _, ok := props["offset"]; ok {
		t.Fatalf("read_documents still advertises offset: %v", props)
	}
}

// TestResearchReadsEveryRequestedID: read_documents used to slice the ids at
// twenty per call, so a model that asked for everything it had found was
// silently answered about part of it.
func TestResearchReadsEveryRequestedID(t *testing.T) {
	t.Parallel()
	const n = 25
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("doc%d", i+1)
	}
	args, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"everything"}`}}},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: string(args)}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "done"},
	)

	var readIDs []string
	_, err = agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "read them all"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor(ids...), nil
		},
		Read: func(_ context.Context, req ReadRequest) ([]DocumentContent, error) {
			readIDs = append([]string{}, req.IDs...)
			docs := make([]DocumentContent, 0, len(req.IDs))
			for _, id := range req.IDs {
				docs = append(docs, DocumentContent{ID: id, Title: "Doc " + id, Text: "text"})
			}
			return docs, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(readIDs) != n {
		t.Fatalf("read %d ids, want all %d — a per-call cap is hiding documents", len(readIDs), n)
	}
}

func TestSearchPromptAnswersFromPassages(t *testing.T) {
	t.Parallel()
	prompt := buildSearchSystemPrompt("en,de", "en", nil, false)
	for _, want := range []string{
		"verbatim passages",
		"When a passage literally contains the answer",
		"suggest Research mode",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("search prompt missing %q: %s", want, prompt)
		}
	}
	if desc := searchDocumentsTools()[0].Function.Description.Value; !strings.Contains(desc, "verbatim passages") {
		t.Fatalf("search_documents description does not promise passages: %q", desc)
	}
}

func TestValidateCitationsToleratesAPageAnchor(t *testing.T) {
	t.Parallel()
	seen := map[string]struct{}{"real1": {}}
	got := validateCitations("See [Invoice](/document/real1?page=3).", seen)
	if !strings.Contains(got, "/document/real1?page=3") {
		t.Fatalf("a page anchor unwrapped a real citation: %q", got)
	}
	got = validateCitations("See [Nope](/document/fake9?page=3).", seen)
	if strings.Contains(got, "/document/") {
		t.Fatalf("an invented citation survived its page anchor: %q", got)
	}
}
