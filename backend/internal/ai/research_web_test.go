package ai

import (
	"context"
	"strings"
	"testing"
)

// The web tools are declared on every research turn and the prompt explains
// them once, so neither moves under the provider's cache. What the per-turn
// toggle decides is whether a call is served or refused.

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

// The tool list is part of what the provider cached and the web toggle is per
// turn, so the list cannot follow the toggle: it would forfeit the transcript's
// prefix on every change. The schemas are always declared, and a turn without
// the web answers them with a refusal instead of not offering them.
func TestTheWebToolsAreDeclaredWhicheverWayTheToggleIs(t *testing.T) {
	t.Parallel()

	want := []string{"search_documents", "read_documents", "web_search", "web_fetch"}

	h, _ := researchWithWeb(t, ResearchRequest{},
		scriptedTurn{content: "ready"}, scriptedTurn{content: "Nothing."})
	off := toolNames(t, h.request(0))
	for _, tool := range want {
		if _, ok := off[tool]; !ok {
			t.Errorf("%s missing with the web off: %v", tool, off)
		}
	}

	web, _ := newWebServer(t, `{"results":[]}`, `{}`)
	h, _ = researchWithWeb(t, ResearchRequest{Web: web},
		scriptedTurn{content: "ready"}, scriptedTurn{content: "Nothing."})
	on := toolNames(t, h.request(0))
	for _, tool := range want {
		if _, ok := on[tool]; !ok {
			t.Errorf("%s missing with the web on: %v", tool, on)
		}
	}
	if len(on) != len(off) {
		t.Fatalf("the toggle changed the tool list: %v with the web, %v without", on, off)
	}
}

// What the toggle does move is the refusal: the model can reach for the web on
// a turn that does not have it, and has to be told so in a way it can act on
// rather than being left to guess why nothing came back.
func TestAWebCallOnATurnWithoutTheWebIsRefused(t *testing.T) {
	t.Parallel()

	h, _ := researchWithWeb(t, ResearchRequest{},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "web_search", args: `{"query":"acme rates"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "The archive does not say."},
	)
	fed := toolMessageContent(t, h.request(1), "web_search")
	if !strings.Contains(fed, "not enabled for this question") {
		t.Fatalf("the refusal does not say why: %s", fed)
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
		Search: func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Read:   func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
		Web:    web,
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

// The web tools are declared on every turn, so the prompt that explains them
// belongs with the rest of the conversation's opening instructions -- written
// once, replayed verbatim, never moved. What varies per turn is only whether a
// call is served, and the refusal says so at the point it happens.
func TestTheOpeningPromptExplainsTheWebTools(t *testing.T) {
	t.Parallel()

	opening := buildResearchSystemPrompt("en", "en", []string{"invoice"}, false)
	for _, want := range []string{
		"web_search",
		"web_fetch",
		// The archive stays the primary source, or the cheap answer is to search
		// the web and skip the documents entirely.
		"archive is still the primary source",
		// A snippet is a reason to fetch, not evidence -- the same rule
		// search_documents has about its passages.
		"web_fetch before claiming what a page contains",
		// And what to do when the call comes back refused, or the model spends
		// the run rediscovering that the web is off.
		"not enabled",
		// The archive instructions are still there.
		"read_documents",
	} {
		if !strings.Contains(opening, want) {
			t.Errorf("opening prompt missing %q: %s", want, opening)
		}
	}
}

// The budget lives on the run's state, so one turn of a conversation cannot
// spend another's allowance.
func TestEachResearchRunGetsItsOwnWebBudget(t *testing.T) {
	t.Parallel()
	web, calls := newWebServer(t, `{"results":[{"title":"t","url":"https://example.com/a","content":"c"}]}`, `{}`)

	for range 2 {
		state := &researchState{}
		for range maxWebCalls + 2 {
			runWebTool(context.Background(), web, &state.web, "c1", "web_search", `{"query":"x"}`, nil)
		}
	}
	if got := calls.Load(); got != int64(2*maxWebCalls) {
		t.Fatalf("provider calls = %d, want %d -- each run spends its own %d", got, 2*maxWebCalls, maxWebCalls)
	}
}
