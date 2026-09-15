package ai

import (
	"strings"

	"github.com/openai/openai-go"
)

// ToolCall is one call an assistant turn made, in the shape the stored thread
// keeps it: the provider's own id, the function name, and the arguments as the
// model wrote them. Arguments stay a string rather than a decoded object so a
// replayed call is byte-identical to the one that was sent, which is the whole
// point of storing it.
type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ThreadMessage is one entry of a research conversation as it was sent to the
// provider. ChatMessage is the two-field view the one-round surfaces use; this
// is the whole array, tool calls and results included, so a follow-up turn
// replays the work rather than repeating it.
type ThreadMessage struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	Calls   []ToolCall `json:"tool_calls,omitempty"`
	// CallID is set on a tool result and names the call it answers.
	CallID string `json:"tool_call_id,omitempty"`
}

// Param renders the message for the provider. The union is the codebase's
// lingua franca: each transport already translates it (responsesInputFrom,
// messagesFrom), so storing this shape keeps one representation rather than
// one per endpoint.
func (m ThreadMessage) Param() (openai.ChatCompletionMessageParamUnion, bool) {
	switch m.Role {
	case "system":
		return openai.SystemMessage(m.Content), true
	case "user":
		return openai.UserMessage(m.Content), true
	case "tool":
		// A tool result with no call to answer is the DSML dialect's: those
		// models put their calls in prose, and the results go back as an
		// ordinary user message. Stored as a tool row all the same, because
		// that is what it is -- and so nothing mistakes it for the question.
		if m.CallID == "" {
			return openai.UserMessage(m.Content), true
		}
		return openai.ToolMessage(m.Content, m.CallID), true
	case "assistant":
		if len(m.Calls) == 0 {
			// An assistant turn with neither text nor calls is nothing the
			// model said; sending it back would be inventing a silence.
			if strings.TrimSpace(m.Content) == "" {
				return openai.ChatCompletionMessageParamUnion{}, false
			}
			return openai.AssistantMessage(m.Content), true
		}
		assistant := openai.ChatCompletionAssistantMessageParam{}
		if strings.TrimSpace(m.Content) != "" {
			assistant.Content.OfString = openai.String(m.Content)
		}
		for _, call := range m.Calls {
			assistant.ToolCalls = append(assistant.ToolCalls, openai.ChatCompletionMessageToolCallParam{
				ID: call.ID,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      call.Name,
					Arguments: call.Arguments,
				},
			})
		}
		return openai.ChatCompletionMessageParamUnion{OfAssistant: &assistant}, true
	default:
		return openai.ChatCompletionMessageParamUnion{}, false
	}
}

// Size is what the message costs the context meter, counted the same way for a
// stored message as for one the loop just built.
func (m ThreadMessage) Size() int {
	size := len(m.Content)
	for _, call := range m.Calls {
		size += len(call.Name) + len(call.Arguments)
	}
	return size
}

// TrimThread drops the ends a provider refuses. A stored thread is read through
// a row cap, so its window can open on a tool result whose call was left behind
// and close on a call nothing answered -- both are a 400 rather than a degraded
// answer. Trimming here keeps that rule in one place.
func TrimThread(thread []ThreadMessage) []ThreadMessage {
	start := 0
	for start < len(thread) && thread[start].Role == "tool" {
		start++
	}
	thread = thread[start:]

	// Walk back over any trailing run of calls and results until the answers
	// line up: a call is only replayable with every result it asked for.
	for len(thread) > 0 {
		answered := map[string]struct{}{}
		for _, msg := range thread {
			if msg.Role == "tool" && msg.CallID != "" {
				answered[msg.CallID] = struct{}{}
			}
		}
		dangling := false
		for _, msg := range thread {
			for _, call := range msg.Calls {
				if _, ok := answered[call.ID]; !ok {
					dangling = true
				}
			}
		}
		if !dangling {
			break
		}
		thread = thread[:len(thread)-1]
	}
	return thread
}
