package ai

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// Research reaches the archive through find_documents; search_documents is
// the search page's tool and stays out of the research list.
func TestResearchDeclaresFindAndNotSearch(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t, scriptedTurn{content: "ready"}, scriptedTurn{content: "Nothing."})
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Thread: []ThreadMessage{{Role: "user", Content: "q"}},
		Search: func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Read:   func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
	}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	names := toolNames(t, h.request(0))
	if _, ok := names["search_documents"]; ok {
		t.Fatalf("search_documents should not be declared to research: %v", names)
	}
	for _, tool := range []string{"find_documents", "read_documents", "read_chunks"} {
		if _, ok := names[tool]; !ok {
			t.Fatalf("%s missing: %v", tool, names)
		}
	}
}

func TestFindToolVerifiesThroughTheFinderAndRegistersItsDocuments(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "find_documents", args: `{"query":"leak insurer","question":"what did the insurer write about the leak","tags":["insurance"]}`}}},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_chunks", args: `{"id":"doc1","chunks":[4,3]}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "The insurer [wrote](/document/doc1)."},
	)

	var got FindArgs
	var read ReadRequest
	var events []ResearchEvent
	result, err := agent.Research(context.Background(), ResearchRequest{
		Thread: []ThreadMessage{{Role: "user", Content: "what did the insurer write about the leak?"}},
		Search: func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) {
			t.Fatal("research must not search directly")
			return nil, nil
		},
		Read: func(_ context.Context, req ReadRequest) ([]DocumentContent, error) {
			read = req
			return []DocumentContent{{ID: "doc1", Title: "Letter", Text: "[chunk 3-4]\nthe leak was covered", Excerpted: true, Chunks: []int{3, 4}, ChunkCount: 9}}, nil
		},
		Find: func(_ context.Context, args FindArgs, progress FindProgress) (FindResult, error) {
			got = args
			progress("screen", 1, 3)
			progress("read", 1, 1)
			return FindResult{
				Candidates: 3, Screened: 3, Read: 1,
				Documents: []FindHit{{ID: "doc1", Title: "Letter", Notes: "covers the leak", Quotes: []string{"the leak was covered"}, Chunks: []int{3, 4}, ChunkCount: 9}},
				Hits:      hitsFor("doc1"),
			}, nil
		},
	}, func(ev ResearchEvent) { events = append(events, ev) })
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	if got.Query != "leak insurer" || got.Question != "what did the insurer write about the leak" || len(got.Tags) != 1 || got.MaxDocuments != MaxSurveyDocuments {
		t.Fatalf("finder args = %+v", got)
	}
	if len(read.IDs) != 1 || read.IDs[0] != "doc1" || len(read.Chunks) != 2 || read.Chunks[0] != 3 || read.Chunks[1] != 4 || read.Full {
		t.Fatalf("read request = %+v, want the two chunks sorted", read)
	}
	if len(result.Documents) != 1 || result.Documents[0].ID != "doc1" {
		t.Fatalf("documents = %+v, want the found document in the result list", result.Documents)
	}

	var phases []string
	for _, ev := range events {
		if ev.Type == "step" && ev.Kind == "find" && ev.Status == "progress" {
			phases = append(phases, ev.Phase)
		}
	}
	if strings.Join(phases, ",") != "screen,read" {
		t.Fatalf("progress phases = %v", phases)
	}
	kinds := stepKinds(events)
	if !strings.Contains(strings.Join(kinds, ","), "find") {
		t.Fatalf("no find step among %v", kinds)
	}

	var payload struct {
		Candidates int       `json:"candidates"`
		Documents  []FindHit `json:"documents"`
	}
	if err := json.Unmarshal([]byte(toolMessageContent(t, h.request(1), "find_documents")), &payload); err != nil {
		t.Fatalf("decode find payload: %v", err)
	}
	if payload.Candidates != 3 || len(payload.Documents) != 1 || len(payload.Documents[0].Chunks) != 2 || payload.Documents[0].ChunkCount != 9 {
		t.Fatalf("find payload = %+v", payload)
	}
}

func TestFindWithNoFinderIsRefused(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "find_documents", args: `{"query":"x"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "Nothing."},
	)
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Thread: []ThreadMessage{{Role: "user", Content: "q"}},
		Search: func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Read:   func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
	}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if !strings.Contains(toolMessageContent(t, h.request(1), "find_documents"), "not available") {
		t.Fatal("a find with nothing behind it should be refused in the tool result")
	}
}

func TestFullReadIsBoundedByTheContextWindow(t *testing.T) {
	t.Parallel()
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "find_documents", args: `{"query":"lease"}`}}},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: `{"ids":["doc1"],"full":true}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "done"},
	)
	var read ReadRequest
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Thread:        []ThreadMessage{{Role: "user", Content: "summarise the lease"}},
		ContextWindow: 100_000,
		Search:        func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Find: func(context.Context, FindArgs, FindProgress) (FindResult, error) {
			return FindResult{Documents: []FindHit{{ID: "doc1", Title: "Lease"}}, Hits: hitsFor("doc1")}, nil
		},
		Read: func(_ context.Context, req ReadRequest) ([]DocumentContent, error) {
			read = req
			return []DocumentContent{{ID: "doc1", Title: "Lease", Text: "whole lease"}}, nil
		},
	}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if !read.Full || len(read.Chunks) != 0 {
		t.Fatalf("read = %+v, want a full read", read)
	}
	// Window in chars, less what the conversation holds, less the answer's
	// reserve: well under the window and well above zero.
	if read.MaxBytes <= 0 || read.MaxBytes >= 100_000*charsPerToken-answerReserveBytes {
		t.Fatalf("MaxBytes = %d, want the window less the conversation and the reserve", read.MaxBytes)
	}
}

func TestFullReadOfSeveralDocumentsAndOversizedChunkReadsAreRefused(t *testing.T) {
	t.Parallel()
	ords := make([]string, 0, maxReadChunks+1)
	for i := 0; i <= maxReadChunks; i++ {
		ords = append(ords, strconv.Itoa(i))
	}
	many := strings.Join(ords, ",")
	h, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "find_documents", args: `{"query":"lease"}`}}},
		scriptedTurn{toolCalls: []scriptedToolCall{
			{name: "read_documents", args: `{"ids":["doc1","doc2"],"full":true}`},
			{name: "read_chunks", args: `{"id":"doc1","chunks":[` + many + `]}`},
			{name: "read_chunks", args: `{"id":"doc1"}`},
		}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "done"},
	)
	reads := 0
	if _, err := agent.Research(context.Background(), ResearchRequest{
		Thread: []ThreadMessage{{Role: "user", Content: "q"}},
		Search: func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Find: func(context.Context, FindArgs, FindProgress) (FindResult, error) {
			return FindResult{Hits: hitsFor("doc1", "doc2")}, nil
		},
		Read: func(context.Context, ReadRequest) ([]DocumentContent, error) {
			reads++
			return nil, nil
		},
	}, nil); err != nil {
		t.Fatalf("Research: %v", err)
	}
	if reads != 0 {
		t.Fatalf("reader called %d times, want every malformed read refused before it", reads)
	}
	// The harness numbers call ids per turn, so results are matched by content.
	body := h.request(2)
	if !strings.Contains(strings.Join(allToolMessages(body, "read_documents"), "\n"), "one document at a time") {
		t.Fatal("a full read of two documents should be refused")
	}
	content := strings.Join(allToolMessages(body, "read_chunks"), "\n")
	if !strings.Contains(content, "at most") || !strings.Contains(content, "chunks is required") {
		t.Fatalf("chunk reads should be refused for size and for missing chunks: %s", content)
	}
}

// allToolMessages returns every tool result for the named tool in a request.
func allToolMessages(request map[string]any, tool string) []string {
	messages, _ := request["messages"].([]any)
	callIDs := map[string]struct{}{}
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		calls, _ := msg["tool_calls"].([]any)
		for _, c := range calls {
			call, _ := c.(map[string]any)
			fn, _ := call["function"].(map[string]any)
			if fn["name"] == tool {
				callIDs[call["id"].(string)] = struct{}{}
			}
		}
	}
	var out []string
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		if msg["role"] != "tool" {
			continue
		}
		if _, ok := callIDs[msg["tool_call_id"].(string)]; ok {
			out = append(out, msg["content"].(string))
		}
	}
	return out
}

func TestDistillRowsCarryChunks(t *testing.T) {
	t.Parallel()
	rows, err := parseDistillRows(`{"documents":[{"id":"a","relevant":true,"notes":"n","chunks":[4,"2","chunk 4",-1]},{"id":"b","relevant":false}]}`,
		DistillRequest{Question: "q", Docs: []DistillDoc{{ID: "a"}, {ID: "b"}}})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 2 || len(rows[0].Chunks) != 2 || rows[0].Chunks[0] != 2 || rows[0].Chunks[1] != 4 || rows[1].Chunks != nil {
		t.Fatalf("rows = %+v", rows)
	}
	if !strings.Contains(buildDistillSystemPrompt(nil), "[chunk N]") {
		t.Fatal("the distill prompt should explain the markers")
	}
}

func TestScreenPromptAndVerdictsAreLenient(t *testing.T) {
	t.Parallel()
	msg := buildScreenUserMessage("leak", []ScreenDoc{{
		ID: "a", Title: "Letter", TitleOriginal: "Brief", DocumentDate: "2024-01-02", DocumentType: "letter",
		Correspondent: "Insurer", Tags: []string{"insurance", "home"}, People: []string{"J. Doe"}, Purpose: "claim", Summary: "about the leak",
		PageCount: 3, Passages: []string{"the kitchen leak"},
	}})
	for _, want := range []string{"--- document a ---", "Title: Letter", "Original title: Brief", "Date: 2024-01-02", "Type: letter",
		"Correspondent: Insurer", "Tags: insurance, home", "People: J. Doe", "Pages: 3", "Purpose: claim", "Summary: about the leak", "Passage: the kitchen leak"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("screen message lacks %q:\n%s", want, msg)
		}
	}

	rows, err := parseScreenRows(`{"documents":[{"id":"a","verdict":"Yes"},{"id":"b","verdict":"nonsense"},{"id":"c","relevant":false},{"id":"zzz","verdict":"no"}]}`,
		ScreenRequest{Question: "q", Docs: []ScreenDoc{{ID: "a"}, {ID: "b"}, {ID: "c"}}})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got := map[string]string{}
	for _, row := range rows {
		got[row.ID] = row.Verdict
	}
	if got["a"] != VerdictYes || got["b"] != VerdictMaybe || got["c"] != VerdictNo || len(got) != 3 {
		t.Fatalf("verdicts = %v", got)
	}
}
