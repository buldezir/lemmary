package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func stepKinds(events []ResearchEvent) []string {
	var kinds []string
	for _, e := range events {
		if e.Type == "step" {
			kinds = append(kinds, e.Kind+":"+e.Status)
		}
	}
	return kinds
}

func TestResearchSurveysThenCitesTheRows(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "survey_documents", args: `{"query":"invoice 2025","fields":[{"name":"total","type":"number"}]}`}}},
		// The rows made their documents citable and readable without a search.
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "read_documents", args: `{"ids":["inv1"]}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "Total 30 EUR: [Invoice 1](/document/inv1), [Invoice 2](/document/inv2), [Ghost](/document/nope)."},
	)

	var got SurveyArgs
	var readIDs []string
	var events []ResearchEvent
	result, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "How much did I pay in invoices in 2025?"}},
		Search: func(_ context.Context, _ SearchDocumentsArgs) ([]DocumentHit, error) {
			t.Fatal("a survey should not go through search")
			return nil, nil
		},
		Read: func(_ context.Context, req ReadRequest) ([]DocumentContent, error) {
			readIDs = req.IDs
			return []DocumentContent{{ID: "inv1", Title: "Invoice 1", Text: "Total 10 EUR"}}, nil
		},
		Survey: func(_ context.Context, args SurveyArgs, progress func(done, total int)) (SurveyResult, error) {
			got = args
			progress(1, 2)
			progress(2, 2)
			return SurveyResult{
				Candidates: 2,
				Surveyed:   2,
				Rows: []SurveyRow{
					{ID: "inv1", Title: "Invoice 1", Relevant: true, Notes: "10 EUR", Values: map[string]string{"total": "10"}},
					{ID: "inv2", Title: "Invoice 2", Relevant: true, Notes: "20 EUR", Values: map[string]string{"total": "20"}},
				},
				Totals:    []SurveyTotal{{Field: "total", Count: 2, Sum: 30, Avg: 15, Min: 10, Max: 20}},
				Documents: hitsFor("inv1", "inv2"),
			}, nil
		},
	}, func(e ResearchEvent) { events = append(events, e) })
	if err != nil {
		t.Fatalf("Research: %v", err)
	}

	// The question defaults to the user's, the cap to the default.
	if got.Question != "How much did I pay in invoices in 2025?" {
		t.Fatalf("question = %q, want the user's message", got.Question)
	}
	if got.MaxDocuments != DefaultSurveyDocuments || got.Query != "invoice 2025" || len(got.Fields) != 1 || got.Fields[0].Type != "number" {
		t.Fatalf("args = %+v", got)
	}
	if len(readIDs) != 1 || readIDs[0] != "inv1" {
		t.Fatalf("surveyed ids should be readable: read = %v", readIDs)
	}
	if !strings.Contains(result.Reply, "/document/inv2") || strings.Contains(result.Reply, "/document/nope") {
		t.Fatalf("surveyed documents should be citable and invented ones not: %q", result.Reply)
	}
	if len(result.Documents) != 2 {
		t.Fatalf("documents = %+v, want the two relevant rows", result.Documents)
	}

	want := "survey:start,survey:progress,survey:progress,survey:done,read:start,read:done,answer:start,answer:done"
	if kinds := strings.Join(stepKinds(events), ","); kinds != want {
		t.Fatalf("steps = %s, want %s", kinds, want)
	}
	for _, e := range events {
		if e.Kind == "survey" && e.Status == "progress" && (e.Count != 2 || e.Done == 0) {
			t.Fatalf("progress event = %+v", e)
		}
	}

	// The model saw the totals, not raw text.
	toolResult := toolMessageContent(t, h.request(1), "survey_documents")
	var payload map[string]any
	if err := json.Unmarshal([]byte(toolResult), &payload); err != nil {
		t.Fatalf("survey payload: %v\n%s", err, toolResult)
	}
	if payload["count_relevant"] != float64(2) || payload["surveyed"] != float64(2) {
		t.Fatalf("payload = %v", payload)
	}
	if _, ok := payload["totals"]; !ok {
		t.Fatalf("payload lacks totals: %v", payload)
	}
}

func TestResearchSurveyRequiresASelection(t *testing.T) {
	t.Parallel()
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "survey_documents", args: `{"question":"what?"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "Nothing."},
	)
	called := false
	_, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
		Search:   func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Read:     func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
		Survey: func(context.Context, SurveyArgs, func(int, int)) (SurveyResult, error) {
			called = true
			return SurveyResult{}, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if called {
		t.Fatal("a survey with neither query nor ids reached the surveyor")
	}
}

func TestResearchSurveyOnlyAcceptsSeenIDs(t *testing.T) {
	t.Parallel()
	_, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "search_documents", args: `{"query":"x"}`}}},
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "survey_documents", args: `{"ids":["seen","unseen"],"question":"what?"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "Done."},
	)
	var got SurveyArgs
	_, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
		Search: func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) {
			return hitsFor("seen"), nil
		},
		Read: func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
		Survey: func(_ context.Context, args SurveyArgs, _ func(int, int)) (SurveyResult, error) {
			got = args
			return SurveyResult{Surveyed: 1, Rows: []SurveyRow{{ID: "seen", Relevant: true}}}, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if len(got.IDs) != 1 || got.IDs[0] != "seen" {
		t.Fatalf("survey ids = %v, want only the seen one", got.IDs)
	}
}

func TestResearchCountsWithoutSearching(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "count_documents", args: `{"document_type":"invoice","date_from":"2025-01-01","date_to":"2025-12-31","group_by":"Correspondent"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "You have 143 invoices."},
	)
	var got CountArgs
	var events []ResearchEvent
	_, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "how many invoices in 2025?"}},
		Search: func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) {
			t.Fatal("a count should not search")
			return nil, nil
		},
		Read: func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
		Count: func(_ context.Context, args CountArgs) (CountResult, error) {
			got = args
			return CountResult{Count: 143, GroupBy: args.GroupBy, Groups: []CountGroup{{Key: "ACME", Count: 100}, {Key: "Other Co", Count: 43}}}, nil
		},
	}, func(e ResearchEvent) { events = append(events, e) })
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	if got.GroupBy != "correspondent" || got.DocumentType != "invoice" || got.DateFrom != "2025-01-01" {
		t.Fatalf("args = %+v", got)
	}
	want := "count:start,count:done,answer:start,answer:done"
	if kinds := strings.Join(stepKinds(events), ","); kinds != want {
		t.Fatalf("steps = %s, want %s", kinds, want)
	}
	content := toolMessageContent(t, h.request(1), "count_documents")
	if !strings.Contains(content, `"count":143`) || !strings.Contains(content, `"ACME"`) {
		t.Fatalf("count payload = %s", content)
	}
}

func TestResearchRejectsAnUnknownGroupBy(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t,
		scriptedTurn{toolCalls: []scriptedToolCall{{name: "count_documents", args: `{"group_by":"colour"}`}}},
		scriptedTurn{content: "ready"},
		scriptedTurn{content: "Cannot."},
	)
	_, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
		Search:   func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Read:     func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
		Count: func(context.Context, CountArgs) (CountResult, error) {
			t.Fatal("an invalid group_by reached the counter")
			return CountResult{}, nil
		},
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	content := toolMessageContent(t, h.request(1), "count_documents")
	if !strings.Contains(content, "unknown group_by") || !strings.Contains(content, "document_type, correspondent") {
		t.Fatalf("error payload should name the allowed values: %s", content)
	}
}

func TestResearchOffersSurveyAndCountOnlyWhenBacked(t *testing.T) {
	t.Parallel()
	h, agent := newResearchAgent(t, scriptedTurn{content: "ready"}, scriptedTurn{content: "Nothing."})
	_, err := agent.Research(context.Background(), ResearchRequest{
		Messages: []ChatMessage{{Role: "user", Content: "q"}},
		Search:   func(context.Context, SearchDocumentsArgs) ([]DocumentHit, error) { return nil, nil },
		Read:     func(context.Context, ReadRequest) ([]DocumentContent, error) { return nil, nil },
		Count:    func(context.Context, CountArgs) (CountResult, error) { return CountResult{}, nil },
	}, nil)
	if err != nil {
		t.Fatalf("Research: %v", err)
	}
	names := toolNames(t, h.request(0))
	if _, ok := names["count_documents"]; !ok {
		t.Fatalf("count_documents should be offered when a counter is set: %v", names)
	}
	if _, ok := names["survey_documents"]; ok {
		t.Fatalf("survey_documents should not be offered without a surveyor: %v", names)
	}
}

func TestDecodeSurveyArgsCoercesLooseShapes(t *testing.T) {
	t.Parallel()
	args, err := decodeSurveyArgs(`{"query": 2025, "ids": "doc1", "question": "q", "fields": ["amount", {"name": "date", "type": "date"}], "max_documents": "50"}`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if args.Query != "2025" || len(args.IDs) != 1 || args.IDs[0] != "doc1" || args.MaxDocuments != 50 {
		t.Fatalf("args = %+v", args)
	}
	if len(args.Fields) != 2 || args.Fields[0].Name != "amount" || args.Fields[1].Type != "date" {
		t.Fatalf("fields = %+v", args.Fields)
	}
}

// toolMessageContent finds the tool result the agent fed back for the named
// tool in a recorded request.
func toolMessageContent(t *testing.T, request map[string]any, tool string) string {
	t.Helper()
	messages, _ := request["messages"].([]any)
	callIDs := map[string]struct{}{}
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		calls, _ := msg["tool_calls"].([]any)
		for _, c := range calls {
			call, _ := c.(map[string]any)
			fn, _ := call["function"].(map[string]any)
			if fn["name"] == tool {
				callIDs[fmt.Sprint(call["id"])] = struct{}{}
			}
		}
	}
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		if msg["role"] != "tool" {
			continue
		}
		if _, ok := callIDs[fmt.Sprint(msg["tool_call_id"])]; ok {
			return fmt.Sprint(msg["content"])
		}
	}
	t.Fatalf("no tool result for %s in %v", tool, messages)
	return ""
}

func toolNames(t *testing.T, request map[string]any) map[string]struct{} {
	t.Helper()
	tools, _ := request["tools"].([]any)
	names := map[string]struct{}{}
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		fn, _ := tool["function"].(map[string]any)
		names[fmt.Sprint(fn["name"])] = struct{}{}
	}
	return names
}
