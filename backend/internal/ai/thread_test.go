package ai

import (
	"encoding/json"
	"testing"
)

func call(id string) ToolCall {
	return ToolCall{ID: id, Name: "search_documents", Arguments: `{"query":"q"}`}
}

// A window into a stored conversation can open on a tool result whose call was
// left behind it. Providers refuse that outright, so it is trimmed rather than
// sent.
func TestTrimThreadDropsALeadingOrphanResult(t *testing.T) {
	t.Parallel()
	got := TrimThread([]ThreadMessage{
		{Role: "tool", Content: "result", CallID: "call_1"},
		{Role: "tool", Content: "result", CallID: "call_2"},
		{Role: "user", Content: "and what about the other one?"},
	})
	if len(got) != 1 || got[0].Role != "user" {
		t.Fatalf("thread = %+v, want only the question", got)
	}
}

// The other end: a run that died between asking a tool and storing its answer.
func TestTrimThreadDropsATrailingUnansweredCall(t *testing.T) {
	t.Parallel()
	got := TrimThread([]ThreadMessage{
		{Role: "user", Content: "how much?"},
		{Role: "assistant", Calls: []ToolCall{call("call_1")}},
		{Role: "tool", Content: "two documents", CallID: "call_1"},
		{Role: "assistant", Calls: []ToolCall{call("call_2")}},
	})
	if len(got) != 3 {
		t.Fatalf("thread = %d messages, want the answered call kept and the dangling one dropped: %+v", len(got), got)
	}
	if got[len(got)-1].Role != "tool" {
		t.Fatalf("thread ends on %q", got[len(got)-1].Role)
	}
}

func TestTrimThreadKeepsACompleteConversation(t *testing.T) {
	t.Parallel()
	thread := []ThreadMessage{
		{Role: "system", Content: "you are researching"},
		{Role: "user", Content: "how much?"},
		{Role: "assistant", Calls: []ToolCall{call("call_1")}},
		{Role: "tool", Content: "two documents", CallID: "call_1"},
		{Role: "assistant", Content: "EUR 412."},
	}
	if got := TrimThread(thread); len(got) != len(thread) {
		t.Fatalf("thread = %d messages, want all %d: %+v", len(got), len(thread), got)
	}
}

// A pure tool-call turn has no text. Dropping it for that would orphan the
// results that answer it.
func TestThreadMessageParamKeepsATextlessCall(t *testing.T) {
	t.Parallel()
	param, ok := ThreadMessage{Role: "assistant", Calls: []ToolCall{call("call_1")}}.Param()
	if !ok {
		t.Fatal("an assistant turn that only calls tools was dropped")
	}
	encoded, err := json.Marshal(param)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body struct {
		Role      string `json:"role"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode: %v body %s", err, encoded)
	}
	if body.Role != "assistant" || len(body.ToolCalls) != 1 {
		t.Fatalf("param = %s", encoded)
	}
	if body.ToolCalls[0].ID != "call_1" || body.ToolCalls[0].Function.Name != "search_documents" {
		t.Fatalf("call was not replayed as sent: %s", encoded)
	}
	// Arguments stay the string the model wrote, so the replay is byte-identical
	// and the provider's cache still recognises the prefix.
	if body.ToolCalls[0].Function.Arguments != `{"query":"q"}` {
		t.Fatalf("arguments were reshaped: %s", encoded)
	}
}

// The DSML dialect puts calls in prose and takes results back as an ordinary
// user message. Stored as a tool row all the same, so nothing mistakes it for
// the question -- but replayed in the shape those models expect.
func TestThreadMessageParamReplaysADialectResultAsAUserMessage(t *testing.T) {
	t.Parallel()
	param, ok := ThreadMessage{Role: "tool", Content: "results"}.Param()
	if !ok {
		t.Fatal("a call-less tool row was dropped")
	}
	encoded, _ := json.Marshal(param)
	var body struct {
		Role string `json:"role"`
	}
	_ = json.Unmarshal(encoded, &body)
	if body.Role != "user" {
		t.Fatalf("role = %q, want user: %s", body.Role, encoded)
	}
}

func TestThreadMessageParamDropsAnEmptyAssistantTurn(t *testing.T) {
	t.Parallel()
	if _, ok := (ThreadMessage{Role: "assistant"}).Param(); ok {
		t.Fatal("an assistant turn with neither text nor calls was sent back")
	}
}
