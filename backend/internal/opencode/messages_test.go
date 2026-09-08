package opencode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"

	"lemmary/backend/internal/aiprovider"
)

// messagesServer stands in for the /messages endpoint. It records the request
// it was reached with and answers whatever the test supplies.
type messagesServer struct {
	body   map[string]any
	header http.Header
	path   string
}

func (m *messagesServer) start(t *testing.T, reply any) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.header = r.Header.Clone()
		m.path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&m.body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// textReply is the smallest well-formed Message.
func textReply(text string) map[string]any {
	return map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant",
		"model": "minimax-m3", "stop_reason": "end_turn",
		"content": []any{map[string]any{"type": "text", "text": text}},
		"usage":   map[string]any{"input_tokens": 11, "output_tokens": 3, "cache_read_input_tokens": 7},
	}
}

func fieldOf(t *testing.T, body map[string]any, key string) any {
	t.Helper()
	value, ok := body[key]
	if !ok {
		t.Fatalf("request has no %q; body was %v", key, body)
	}
	return value
}

// The shape the two APIs disagree about most: a system message is a parameter
// there, not a role, so left in the message list it would either be rejected or
// silently read as a user turn -- and every prompt in this codebase is a system
// message.
func TestSystemPromptIsHoistedOutOfTheMessageList(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewMessages("k", base, time.Second)

	resp, err := CompleteViaMessages(context.Background(), client, nil, base, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("you transcribe documents"),
			openai.UserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Errorf("content = %q, want %q", resp.Choices[0].Message.Content, "ok")
	}
	if srv.path != "/v1/messages" {
		t.Errorf("path = %q, want /v1/messages", srv.path)
	}

	system, _ := json.Marshal(fieldOf(t, srv.body, "system"))
	if !strings.Contains(string(system), "you transcribe documents") {
		t.Errorf("system = %s, want the system message", system)
	}
	messages, ok := fieldOf(t, srv.body, "messages").([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages = %v, want only the user turn", srv.body["messages"])
	}
	if role := messages[0].(map[string]any)["role"]; role != "user" {
		t.Errorf("the one message has role %v, want user", role)
	}
}

// max_tokens is required by the Messages API and set by nobody in this
// codebase, so the translation has to invent one. Without it every request is
// rejected before a model sees it.
func TestMaxTokensIsAlwaysSent(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewMessages("k", base, time.Second)

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}
	if _, err := CompleteViaMessages(context.Background(), client, nil, base, params); err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}
	if got := fieldOf(t, srv.body, "max_tokens"); got != float64(defaultMaxTokens) {
		t.Errorf("max_tokens = %v, want the default %d", got, defaultMaxTokens)
	}

	// A caller that does name one is not overridden by it.
	params.MaxTokens = openai.Int(64)
	if _, err := CompleteViaMessages(context.Background(), client, nil, base, params); err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}
	if got := fieldOf(t, srv.body, "max_tokens"); got != float64(64) {
		t.Errorf("max_tokens = %v, want the caller's 64", got)
	}
}

// The Messages API has no bare json_object mode -- its structured output wants
// a full schema, which none of these callers has. Sent anyway it would be an
// unknown parameter; dropped, the prompts' own "Return one JSON object" and the
// lenient parsers carry it, exactly as they do when /chat/completions rejects
// the parameter.
func TestJSONModeIsDroppedRatherThanSentUntranslated(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply(`{"ok":true}`))
	client := NewMessages("k", base, time.Second)

	_, err := CompleteViaMessages(context.Background(), client, nil, base, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
	})
	if err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}
	for _, key := range []string{"response_format", "output_config"} {
		if _, present := srv.body[key]; present {
			t.Errorf("request carries %q: %v", key, srv.body[key])
		}
	}
}

// A research turn replays its whole thread every round, tool calls included. On
// this API a call is a content block on the assistant turn and its answer is a
// tool_result block on a user turn -- not an assistant field and a tool-role
// message. Getting this wrong loses the tool results, so the model re-runs the
// same searches forever.
func TestToolCallsRoundTripThroughContentBlocks(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, map[string]any{
		"id": "msg_2", "type": "message", "role": "assistant",
		"model": "minimax-m3", "stop_reason": "tool_use",
		"content": []any{
			map[string]any{"type": "text", "text": "looking"},
			map[string]any{"type": "tool_use", "id": "call_9", "name": "search", "input": map[string]any{"q": "rent"}},
		},
		"usage": map[string]any{"input_tokens": 5, "output_tokens": 2},
	})
	client := NewMessages("k", base, time.Second)

	resp, err := CompleteViaMessages(context.Background(), client, nil, base, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("find the rent"),
			{OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{{
					ID: "call_1",
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name: "search", Arguments: `{"q":"rent"}`,
					},
				}},
			}},
			openai.ToolMessage("two hits", "call_1"),
		},
		Tools: []openai.ChatCompletionToolParam{{
			Function: shared.FunctionDefinitionParam{
				Name:        "search",
				Description: openai.String("search the archive"),
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{"q": map[string]any{"type": "string"}},
					"required":   []string{"q"},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}

	// Outbound: the call became a tool_use block, its answer a user turn with a
	// tool_result block.
	sent, _ := json.Marshal(srv.body["messages"])
	for _, want := range []string{`"type":"tool_use"`, `"type":"tool_result"`, `"tool_use_id":"call_1"`} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("messages missing %s: %s", want, sent)
		}
	}
	// The schema arrived whole, not just its named fields.
	tools, _ := json.Marshal(srv.body["tools"])
	for _, want := range []string{`"name":"search"`, `"input_schema"`, `"required":["q"]`} {
		if !strings.Contains(string(tools), want) {
			t.Errorf("tools missing %s: %s", want, tools)
		}
	}

	// Inbound: the tool_use block became a chat tool call the callers read.
	calls := resp.Choices[0].Message.ToolCalls
	if len(calls) != 1 || calls[0].ID != "call_9" || calls[0].Function.Name != "search" {
		t.Fatalf("tool calls = %+v, want the one from the reply", calls)
	}
	if calls[0].Function.Arguments != `{"q":"rent"}` {
		t.Errorf("arguments = %q, want the input as JSON", calls[0].Function.Arguments)
	}
	if resp.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", resp.Choices[0].FinishReason)
	}
}

// LLM OCR is the reason this translation handles content parts at all: it sends
// the document as a data URI in an image_url or file part, and the Messages API
// wants the media type and the base64 payload as separate fields.
func TestOCRPartsBecomeImageAndDocumentBlocks(t *testing.T) {
	t.Parallel()
	png := base64.StdEncoding.EncodeToString([]byte("not really a png"))
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4"))

	for _, tc := range []struct {
		name string
		part openai.ChatCompletionContentPartUnionParam
		want []string
	}{
		{
			name: "image",
			part: openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
				URL: "data:image/png;base64," + png,
			}),
			want: []string{`"type":"image"`, `"media_type":"image/png"`, `"data":"` + png + `"`},
		},
		{
			name: "pdf",
			part: openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
				Filename: openai.String("statement.pdf"),
				FileData: openai.String("data:application/pdf;base64," + pdf),
			}),
			want: []string{`"type":"document"`, `"media_type":"application/pdf"`, `"data":"` + pdf + `"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := &messagesServer{}
			base := srv.start(t, textReply("transcribed"))
			client := NewMessages("k", base, time.Second)

			_, err := CompleteViaMessages(context.Background(), client, nil, base, openai.ChatCompletionNewParams{
				Model: shared.ChatModel("minimax-m3"),
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
						openai.TextContentPart("read this"), tc.part,
					}),
				},
			})
			if err != nil {
				t.Fatalf("CompleteViaMessages: %v", err)
			}
			sent, _ := json.Marshal(srv.body["messages"])
			for _, want := range tc.want {
				if !strings.Contains(string(sent), want) {
					t.Errorf("messages missing %s: %s", want, sent)
				}
			}
		})
	}
}

// A docx reaches the Messages API as neither an image nor a PDF, and the only
// base64 document source it has is a PDF one. Refused here it names the
// problem; sent, it comes back as an opaque upstream error.
func TestANonPDFDocumentIsRefusedWithAUsefulError(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("unused"))
	client := NewMessages("k", base, time.Second)

	_, err := CompleteViaMessages(context.Background(), client, nil, base, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
				openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
					FileData: openai.String("data:application/vnd.openxmlformats-officedocument.wordprocessingml.document;base64,eA=="),
				}),
			}),
		},
	})
	if err == nil {
		t.Fatal("a docx was accepted; want an error naming the mime type")
	}
	if !strings.Contains(err.Error(), "wordprocessingml") {
		t.Errorf("error = %v, want it to name the mime type", err)
	}
}

// The header OpenCode requires. It is the whole reason this SDK exists
// separately, and the Anthropic client needs its own middleware for it because
// the two SDKs' option types are unrelated -- so an untested one is an easy
// thing to leave off.
func TestTheSessionHeaderIsSentOnMessagesToo(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewMessages("k", base, time.Second)

	ctx := aiprovider.WithSession(context.Background(), "session-abc")
	if _, err := CompleteViaMessages(ctx, client, nil, base, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}
	if got := srv.header.Get(aiprovider.SessionHeader); got != "session-abc" {
		t.Errorf("%s = %q, want the session on the context", aiprovider.SessionHeader, got)
	}
	// Both credential headers, because the docs name neither.
	if got := srv.header.Get("X-Api-Key"); got != "k" {
		t.Errorf("X-Api-Key = %q, want the key", got)
	}
	if got := srv.header.Get("Authorization"); got != "Bearer k" {
		t.Errorf("Authorization = %q, want a bearer token", got)
	}
}

// Usage is what a Deep Search run is budgeted against, and the two APIs name
// the numbers differently -- cache_read_input_tokens is the cached part of the
// input, included in it rather than additional to it.
func TestUsageIsFoldedIntoTheChatCompletionShape(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewMessages("k", base, time.Second)

	resp, err := CompleteViaMessages(context.Background(), client, nil, base, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("CompleteViaMessages: %v", err)
	}
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 3 {
		t.Errorf("usage = %+v, want 11 prompt and 3 completion", resp.Usage)
	}
	if resp.Usage.PromptTokensDetails.CachedTokens != 7 {
		t.Errorf("cached = %d, want 7", resp.Usage.PromptTokensDetails.CachedTokens)
	}
	if resp.Usage.TotalTokens != 14 {
		t.Errorf("total = %d, want 14; the API reports no total of its own", resp.Usage.TotalTokens)
	}
}

// Chat streams the answer a token at a time, and the deltas arrive under
// different event names on this API.
func TestStreamingDeltasAndUsage(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// The SSE event name matters here, unlike on the Responses stream: the
		// SDK's decoder dispatches on it and drops an event it does not know.
		for _, event := range [][2]string{
			{"message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","model":"minimax-m3","content":[],"usage":{"input_tokens":9,"output_tokens":0}}}`},
			{"content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hel"}}`},
			{"content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"lo"}}`},
			{"content_block_stop", `{"type":"content_block_stop","index":0}`},
			{"message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":9,"output_tokens":2,"cache_read_input_tokens":4}}`},
			{"message_stop", `{"type":"message_stop"}`},
		} {
			_, _ = w.Write([]byte("event: " + event[0] + "\ndata: " + event[1] + "\n\n"))
		}
	}))
	t.Cleanup(srv.Close)
	client := NewMessages("k", srv.URL, time.Second)

	var seen []string
	text, usage, err := CompleteStreamingViaMessages(context.Background(), client, nil, srv.URL,
		openai.ChatCompletionNewParams{
			Model:    shared.ChatModel("minimax-m3"),
			Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
		},
		func(delta string) { seen = append(seen, delta) },
	)
	if err != nil {
		t.Fatalf("CompleteStreamingViaMessages: %v", err)
	}
	if text != "Hello" {
		t.Errorf("text = %q, want %q", text, "Hello")
	}
	if strings.Join(seen, "|") != "Hel|lo" {
		t.Errorf("deltas = %v, want them handed over as they arrived", seen)
	}
	// message_delta is cumulative and has the last word.
	if usage.PromptTokens != 9 || usage.CompletionTokens != 2 || usage.PromptTokensDetails.CachedTokens != 4 {
		t.Errorf("usage = %+v, want 9/2/4 from message_delta", usage)
	}
}
