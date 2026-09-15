package ai

import (
	"context"
	"strings"
	"testing"
)

// Everything the research loop shows the model changes with whether a
// web-search provider is bound. Off is the pre-flag state and has to stay
// silent about tools nobody can call: a prompt that advertises web_search on an
// instance that cannot serve it spends a round being refused.

func researchWithWeb(t *testing.T, req ResearchRequest, turns ...scriptedTurn) (*researchHarness, ResearchResult) {
	t.Helper()
	h, agent := newResearchAgent(t, turns...)
	req.Thread = []ThreadMessage{{Role: "user", Content: "What does Acme charge now?"}}
	req.Search = func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil }
	req.Read = func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil }
	result, err := agent.Research(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	return h, result
}

func TestResearchOffersTheWebToolsOnlyWhenBacked(t *testing.T) {
	t.Parallel()

	h, _ := researchWithWeb(t, ResearchRequest{},
		scriptedTurn{content: "ready"}, scriptedTurn{content: "Nothing."})
	names := toolNames(t, h.request(0))
	for _, tool := range []string{"web_search", "web_fetch"} {
		if _, ok := names[tool]; ok {
			t.Fatalf("%s offered with no provider bound: %v", tool, names)
		}
	}
	// The archive tools are unaffected by the feature being off.
	if _, ok := names["search_documents"]; !ok {
		t.Fatalf("archive tools missing: %v", names)
	}

	web, _ := newWebServer(t, `{"results":[]}`, `{}`)
	h, _ = researchWithWeb(t, ResearchRequest{Web: web},
		scriptedTurn{content: "ready"}, scriptedTurn{content: "Nothing."})
	names = toolNames(t, h.request(0))
	for _, tool := range []string{"search_documents", "read_documents", "web_search", "web_fetch"} {
		if _, ok := names[tool]; !ok {
			t.Errorf("%s missing with a provider bound: %v", tool, names)
		}
	}
}

// A caller that stores nothing gets the same two rows the handler would have
// written: the opening prompt, then the web instruction beside it. Two messages
// rather than one string, because only one of them belongs to the conversation.
func TestResearchSendsTheWebInstructionAsItsOwnMessage(t *testing.T) {
	t.Parallel()

	web, _ := newWebServer(t, `{"results":[]}`, `{}`)
	h, _ := researchWithWeb(t, ResearchRequest{Web: web},
		scriptedTurn{content: "ready"}, scriptedTurn{content: "Nothing."})

	messages, _ := h.request(0)["messages"].([]any)
	if len(messages) < 3 {
		t.Fatalf("messages = %v", messages)
	}
	first, _ := messages[0].(map[string]any)
	second, _ := messages[1].(map[string]any)
	if first["role"] != "system" || second["role"] != "system" {
		t.Fatalf("the opening rows are %v and %v, want two system messages", first["role"], second["role"])
	}
	opening, _ := first["content"].(string)
	if strings.Contains(opening, "web_search") {
		t.Fatalf("the opening prompt carries the per-turn web instruction: %s", opening)
	}
	if got, _ := second["content"].(string); got != researchWebPrompt() {
		t.Fatalf("second message = %q, want the web prompt", got)
	}
}

// The whole leg end to end: the model calls web_search, the result is fed back
// as a tool message, and the answer keeps the URL. validateCitations strips
// document links to ids never seen, and an https link has to survive that.
func TestResearchFeedsWebResultsBackAndKeepsTheCitation(t *testing.T) {
	t.Parallel()
	web, calls := newWebServer(t,
		`{"results":[{"title":"Acme rates","url":"https://example.com/rates","content":"95 EUR an hour"}]}`, `{}`)

	h, result := researchWithWeb(t, ResearchRequest{Web: web},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "web_search", args: `{"query":"acme rates"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "95 EUR an hour, per [Acme rates](https://example.com/rates)."},
	)

	if got := calls.Load(); got != 1 {
		t.Fatalf("provider calls = %d, want 1", got)
	}
	fed := toolMessageContent(t, h.request(1), "web_search")
	if !strings.Contains(fed, "https://example.com/rates") {
		t.Errorf("web result not fed back to the model: %s", fed)
	}
	if !strings.Contains(result.Reply, "https://example.com/rates") {
		t.Errorf("answer lost its web citation: %s", result.Reply)
	}
}

// Steps are what the page renders and what is stored on the turn, so a web
// round has to report itself the way a search or a read does.
func TestResearchEmitsWebSteps(t *testing.T) {
	t.Parallel()
	web, _ := newWebServer(t,
		`{"results":[{"title":"Acme rates","url":"https://example.com/rates","content":"95 EUR"}]}`,
		`{"results":[{"url":"https://example.com/rates","raw_content":"95 EUR an hour"}]}`)

	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{
			{name: "web_search", args: `{"query":"acme rates"}`},
			{name: "web_fetch", args: `{"urls":["https://example.com/rates"]}`},
		}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "95 EUR an hour."},
	)

	var kinds []string
	_, err := agent.Research(context.Background(), ResearchRequest{
		Thread: []ThreadMessage{{Role: "user", Content: "q"}},
		Search:   func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Read:     func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
		Web:      web,
	}, func(ev ResearchEvent) {
		if ev.Type == "step" && ev.Status == "done" {
			kinds = append(kinds, ev.Kind)
		}
	})
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	joined := strings.Join(kinds, ",")
	for _, want := range []string{"web_search", "web_fetch"} {
		if !strings.Contains(joined, want) {
			t.Errorf("no %s step emitted: %v", want, kinds)
		}
	}
}

func TestResearchAnswerInstructionAsksForWebCitationsOnlyWithTheWeb(t *testing.T) {
	t.Parallel()

	without := researchAnswerInstruction(false)
	if strings.Contains(without, "https://") {
		t.Fatalf("answer instruction asks for web citations with no web: %s", without)
	}
	with := researchAnswerInstruction(true)
	if !strings.Contains(with, "https://") {
		t.Errorf("answer instruction does not say how to cite a page: %s", with)
	}
	// Which half a claim came from is what a reader cannot recover later.
	if !strings.Contains(with, "which claims came from the web") {
		t.Errorf("answer instruction does not ask the two sources to be told apart: %s", with)
	}
	// The archive citation format survives in both.
	for _, instruction := range []string{without, with} {
		if !strings.Contains(instruction, "/document/<id>") {
			t.Errorf("answer instruction dropped the document citation format: %s", instruction)
		}
	}
}

// The web toggle is per turn and the system prompt is per conversation, so the
// two cannot be the same string: the opening prompt never mentions the web, and
// what does is recorded beside the question of a turn that carries the tools.
func TestTheWebInstructionIsSeparateFromTheOpeningPrompt(t *testing.T) {
	t.Parallel()

	opening := buildResearchSystemPrompt("en", "en", []string{"invoice"}, false)
	if strings.Contains(opening, "web_search") || strings.Contains(opening, "web_fetch") {
		t.Fatalf("the opening prompt advertises tools a later turn may not offer: %s", opening)
	}
	if !strings.Contains(opening, "read_documents") {
		t.Error("the opening prompt dropped the archive instructions")
	}

	web := researchWebPrompt()
	for _, want := range []string{
		"web_search",
		"web_fetch",
		// The archive stays the primary source, or the cheap answer is to search
		// the web and skip the documents entirely.
		"archive is still the primary source",
		// A snippet is a reason to fetch, not evidence -- the same rule
		// search_documents has about its passages.
		"web_fetch before claiming what a page contains",
	} {
		if !strings.Contains(web, want) {
			t.Errorf("web prompt missing %q: %s", want, web)
		}
	}
}

// The budget lives on the run's state, so one turn of a conversation cannot
// spend another's allowance.
func TestEachResearchRunGetsItsOwnWebBudget(t *testing.T) {
	t.Parallel()
	web, calls := newWebServer(t, `{"results":[{"title":"t","url":"https://example.com/a","content":"c"}]}`, `{}`)

	for run := 0; run < 2; run++ {
		state := &researchState{}
		for i := 0; i < maxWebCalls+2; i++ {
			runWebTool(context.Background(), web, &state.web, "c1", "web_search", `{"query":"x"}`, nil)
		}
	}
	if got := calls.Load(); got != int64(2*maxWebCalls) {
		t.Fatalf("provider calls = %d, want %d -- each run spends its own %d", got, 2*maxWebCalls, maxWebCalls)
	}
}
