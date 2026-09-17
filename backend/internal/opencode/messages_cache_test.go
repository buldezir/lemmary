package opencode

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"
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
		Tools: []openai.ChatCompletionToolUnionParam{openai.ChatCompletionFunctionTool(shared.FunctionDefinitionParam{
			Name:       "search_documents",
			Parameters: map[string]any{"type": "object"},
		})},
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
