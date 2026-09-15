package opencode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// This API caches nothing unasked: without a breakpoint every request pays for
// the whole conversation again. Three marks -- the system prompt and the tool
// list are the fixed head, and the end of the last message is the one that
// moves, so each request writes the cache the next one reads.
func TestCacheBreakpointsAreSentOnTheHeadAndTheTail(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewMessages("k", base, time.Second)

	_, err := CompleteViaMessages(context.Background(), client, nil, base, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("you research the archive"),
			openai.UserMessage("how much did I pay?"),
		},
		Tools: []openai.ChatCompletionToolParam{{
			Function: shared.FunctionDefinitionParam{
				Name:       "search_documents",
				Parameters: map[string]any{"type": "object"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}

	const breakpoint = `"cache_control":{"type":"ephemeral"}`
	for _, where := range []string{"system", "tools", "messages"} {
		raw, marshalErr := json.Marshal(fieldOf(t, srv.body, where))
		if marshalErr != nil {
			t.Fatalf("marshal %s: %v", where, marshalErr)
		}
		if !strings.Contains(string(raw), breakpoint) {
			t.Errorf("%s carries no cache breakpoint: %s", where, raw)
		}
	}
}

// Two web-on turns of one conversation. The per-turn web note is a user message
// precisely so that it stays one: this API has no per-message system role, so
// every system message is lifted into the top-level system array, where a note
// recorded once per turn would pile up -- and the system array sits ahead of the
// whole transcript in the cached prefix, so a second copy would re-bill every
// tool result the conversation ever gathered.
func TestARepeatedTurnNoteDoesNotGrowTheSystemPrefix(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewMessages("k", base, time.Second)

	const note = "You can also reach the public web with web_search and web_fetch."
	_, err := CompleteViaMessages(context.Background(), client, nil, base, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("you research the archive"),
			openai.UserMessage(note),
			openai.UserMessage("How much did the repair cost?"),
			openai.AssistantMessage("It cost 200 EUR."),
			openai.UserMessage(note),
			openai.UserMessage("And who sent it?"),
		},
	})
	if err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}

	system, ok := fieldOf(t, srv.body, "system").([]any)
	if !ok || len(system) != 1 {
		t.Fatalf("system = %v, want only the conversation's opening prompt", srv.body["system"])
	}
	opening, _ := system[0].(map[string]any)["text"].(string)
	if opening != "you research the archive" {
		t.Fatalf("system block = %q, want the opening prompt alone", opening)
	}

	// And the note is still beside the question it was recorded with, in the
	// last turn rather than hoisted to the head of the prompt.
	messages, _ := fieldOf(t, srv.body, "messages").([]any)
	if len(messages) == 0 {
		t.Fatal("no messages sent")
	}
	last, marshalErr := json.Marshal(messages[len(messages)-1])
	if marshalErr != nil {
		t.Fatalf("marshal the last turn: %v", marshalErr)
	}
	for _, want := range []string{note, "And who sent it?"} {
		if !strings.Contains(string(last), want) {
			t.Errorf("the last turn lost %q: %s", want, last)
		}
	}
}
