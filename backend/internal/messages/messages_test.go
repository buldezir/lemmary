package messages

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

// A system message is a parameter there, not a role, so left in the message
// list it would be rejected or silently read as a user turn.
func TestSystemPromptIsHoistedOutOfTheMessageList(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	resp, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("you transcribe documents"),
			openai.UserMessage("hello"),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
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

func TestMaxTokensIsAlwaysSent(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	params := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}
	if _, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, params); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := fieldOf(t, srv.body, "max_tokens"); got != float64(defaultMaxTokens) {
		t.Errorf("max_tokens = %v, want the default %d", got, defaultMaxTokens)
	}

	// A caller that does name one is not overridden by it.
	params.MaxTokens = openai.Int(64)
	if _, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, params); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got := fieldOf(t, srv.body, "max_tokens"); got != float64(64) {
		t.Errorf("max_tokens = %v, want the caller's 64", got)
	}
}

func TestJSONModeIsDroppedRatherThanSentUntranslated(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply(`{"ok":true}`))
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	_, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	for _, key := range []string{"response_format", "output_config"} {
		if _, present := srv.body[key]; present {
			t.Errorf("request carries %q: %v", key, srv.body[key])
		}
	}
}

// On this API a tool call is a content block on the assistant turn and its
// answer a tool_result block on a user turn. Getting this wrong loses the tool
// results, so the model re-runs the same searches forever.
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
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	resp, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
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
		t.Fatalf("Complete: %v", err)
	}

	sent, _ := json.Marshal(srv.body["messages"])
	for _, want := range []string{`"type":"tool_use"`, `"type":"tool_result"`, `"tool_use_id":"call_1"`} {
		if !strings.Contains(string(sent), want) {
			t.Errorf("messages missing %s: %s", want, sent)
		}
	}
	tools, _ := json.Marshal(srv.body["tools"])
	for _, want := range []string{`"name":"search"`, `"input_schema"`, `"required":["q"]`} {
		if !strings.Contains(string(tools), want) {
			t.Errorf("tools missing %s: %s", want, tools)
		}
	}

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

// LLM OCR sends the document as a data URI in an image_url or file part, and
// the Messages API wants the media type and the base64 payload separately.
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
			client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

			_, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
				Model: shared.ChatModel("minimax-m3"),
				Messages: []openai.ChatCompletionMessageParamUnion{
					openai.UserMessage([]openai.ChatCompletionContentPartUnionParam{
						openai.TextContentPart("read this"), tc.part,
					}),
				},
			})
			if err != nil {
				t.Fatalf("Complete: %v", err)
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

func TestANonPDFDocumentIsRefusedWithAUsefulError(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("unused"))
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	_, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
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

// The header OpenCode requires. The Anthropic client needs its own middleware
// for it, because the two SDKs' option types are unrelated.
func TestTheSessionHeaderIsSentOnMessagesToo(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	ctx := aiprovider.WithSession(context.Background(), "session-abc")
	if _, err := Complete(ctx, client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	}); err != nil {
		t.Fatalf("Complete: %v", err)
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
	// The openai-go option that names us elsewhere does not reach this client.
	if got := srv.header.Get("User-Agent"); got != aiprovider.UserAgent {
		t.Errorf("User-Agent = %q, want %q", got, aiprovider.UserAgent)
	}
}

func TestUsageIsFoldedIntoTheChatCompletionShape(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	resp, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
		Model:    shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
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
	client := NewClient(aiprovider.SDKOpenCode, "k", srv.URL, time.Second)

	var seen []string
	text, usage, err := CompleteStreaming(context.Background(), client, nil, aiprovider.SDKOpenCode, srv.URL, Options{},
		openai.ChatCompletionNewParams{
			Model:    shared.ChatModel("minimax-m3"),
			Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
		},
		func(delta string) { seen = append(seen, delta) },
	)
	if err != nil {
		t.Fatalf("CompleteStreaming: %v", err)
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

// This is the shape research.go builds: a ToolMessage per parallel call, then
// a UserMessage. All three are user turns here, and a tool_use left unanswered
// in the turn immediately after it is a 400 where the gateway does not combine
// them for us.
func TestParallelToolResultsLandInOneUserTurn(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	_, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("how much did I pay?"),
			{OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{ID: "call_1", Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name: "search_documents", Arguments: `{"q":"rent"}`}},
					{ID: "call_2", Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Name: "read_documents", Arguments: `{"ids":["doc1"]}`}},
				},
			}},
			openai.ToolMessage("two hits", "call_1"),
			openai.ToolMessage("the text", "call_2"),
			openai.UserMessage("now write the answer"),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	messages, ok := srv.body["messages"].([]any)
	if !ok {
		t.Fatalf("messages = %v", srv.body["messages"])
	}
	// user / assistant / user, not user / assistant / user / user / user.
	var roles []string
	for _, m := range messages {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "user,assistant,user" {
		t.Fatalf("roles = %v, want the turns to alternate", roles)
	}

	last, _ := json.Marshal(messages[2])
	for _, want := range []string{`"tool_use_id":"call_1"`, `"tool_use_id":"call_2"`, "now write the answer"} {
		if !strings.Contains(string(last), want) {
			t.Errorf("final user turn missing %s: %s", want, last)
		}
	}
	if i, j := strings.Index(string(last), "call_2"), strings.Index(string(last), "now write"); i > j {
		t.Errorf("the instruction was merged ahead of a tool_result: %s", last)
	}
}

func TestSeparateTurnsAreNotMerged(t *testing.T) {
	t.Parallel()
	srv := &messagesServer{}
	base := srv.start(t, textReply("ok"))
	client := NewClient(aiprovider.SDKOpenCode, "k", base, time.Second)

	_, err := Complete(context.Background(), client, nil, aiprovider.SDKOpenCode, base, Options{}, openai.ChatCompletionNewParams{
		Model: shared.ChatModel("minimax-m3"),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.UserMessage("first"),
			openai.AssistantMessage("answer"),
			openai.UserMessage("second"),
		},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	messages, _ := srv.body["messages"].([]any)
	if len(messages) != 3 {
		t.Fatalf("messages = %d, want the three turns kept apart", len(messages))
	}
}

func TestURLDropsTheVersionSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sdk  string
		base string
		want string
	}{
		{aiprovider.SDKOpenCode, "https://opencode.ai/zen/go/v1", "https://opencode.ai/zen/go/v1/messages"},
		{aiprovider.SDKOpenCode, "https://opencode.ai/zen/go/v1/", "https://opencode.ai/zen/go/v1/messages"},
		// A test server's base URL has no /v1 to strip.
		{aiprovider.SDKOpenCode, "http://127.0.0.1:8080", "http://127.0.0.1:8080/v1/messages"},
		// Empty falls back to the SDK's documented endpoint, which is why the
		// sdk has to travel with the base URL.
		{aiprovider.SDKOpenCode, "", "https://opencode.ai/zen/go/v1/messages"},
		{aiprovider.SDKAnthropic, "", "https://api.anthropic.com/v1/messages"},
		{aiprovider.SDKAnthropic, "https://api.anthropic.com/v1", "https://api.anthropic.com/v1/messages"},
	}
	for _, tc := range cases {
		if got := URL(tc.sdk, tc.base); got != tc.want {
			t.Errorf("URL(%q, %q) = %q, want %q", tc.sdk, tc.base, got, tc.want)
		}
	}
}

// Options are the two fields with no counterpart in the OpenAI-shaped
// parameters. internal/ai decides what goes in them; this is the mapping.
func TestOptionsBecomeOutputConfigAndThinking(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		opts   Options
		effort any
		think  any
	}{
		{"effort low", Options{Effort: "low"}, "low", nil},
		{"effort medium", Options{Effort: "medium"}, "medium", nil},
		{"effort high", Options{Effort: "high"}, "high", nil},
		// "none" has no counterpart on this API; low is as little as Claude
		// thinks.
		{"effort none", Options{Effort: "none"}, "low", nil},
		// The zero value sends neither, which is what every retry falls back
		// to and what opencode always sends.
		{"nothing", Options{}, nil, nil},
		{"thinking off", Options{DisableThinking: true}, nil, "disabled"},
		{"both", Options{Effort: "low", DisableThinking: true}, "low", "disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &messagesServer{}
			base := srv.start(t, textReply("ok"))
			client := NewClient(aiprovider.SDKAnthropic, "k", base, time.Second)

			if _, err := Complete(context.Background(), client, nil, aiprovider.SDKAnthropic, base, tc.opts, openai.ChatCompletionNewParams{
				Model:    shared.ChatModel("m"),
				Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
			}); err != nil {
				t.Fatalf("Complete: %v", err)
			}

			config, _ := srv.body["output_config"].(map[string]any)
			if tc.effort == nil {
				if config != nil {
					t.Errorf("output_config = %v, want none", config)
				}
			} else if config == nil || config["effort"] != tc.effort {
				t.Errorf("output_config = %v, want effort %v", srv.body["output_config"], tc.effort)
			}

			thinking, _ := srv.body["thinking"].(map[string]any)
			if tc.think == nil {
				if thinking != nil {
					t.Errorf("thinking = %v, want none", thinking)
				}
			} else if thinking == nil || thinking["type"] != tc.think {
				t.Errorf("thinking = %v, want type %v", srv.body["thinking"], tc.think)
			}
		})
	}
}

// The bearer is OpenCode's: its other endpoints take one. A bearer beside the
// key is how an OAuth request is shaped, and this is not one.
func TestTheBearerIsOpenCodesAlone(t *testing.T) {
	t.Parallel()
	for sdk, wantBearer := range map[string]bool{
		aiprovider.SDKOpenCode:  true,
		aiprovider.SDKAnthropic: false,
	} {
		srv := &messagesServer{}
		base := srv.start(t, textReply("ok"))
		client := NewClient(sdk, "k", base, time.Second)

		if _, err := Complete(context.Background(), client, nil, sdk, base, Options{}, openai.ChatCompletionNewParams{
			Model:    shared.ChatModel("m"),
			Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
		}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if got := srv.header.Get("x-api-key"); got != "k" {
			t.Errorf("%s sent x-api-key = %q, want the key", sdk, got)
		}
		if got := srv.header.Get("Authorization") != ""; got != wantBearer {
			t.Errorf("%s sent an Authorization bearer = %v, want %v", sdk, got, wantBearer)
		}
	}
}

// The session header is OpenCode's routing key. Anthropic would only see an
// unknown header carrying a conversation id on every request.
func TestTheSessionHeaderIsOpenCodesAlone(t *testing.T) {
	t.Parallel()
	for sdk, want := range map[string]string{
		aiprovider.SDKOpenCode:  "conv123",
		aiprovider.SDKAnthropic: "",
	} {
		srv := &messagesServer{}
		base := srv.start(t, textReply("ok"))
		client := NewClient(sdk, "k", base, time.Second)

		ctx := aiprovider.WithSession(context.Background(), "conv123")
		if _, err := Complete(ctx, client, nil, sdk, base, Options{}, openai.ChatCompletionNewParams{
			Model:    shared.ChatModel("m"),
			Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("hi")},
		}); err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if got := srv.header.Get(aiprovider.SessionHeader); got != want {
			t.Errorf("%s sent %s = %q, want %q", sdk, aiprovider.SessionHeader, got, want)
		}
	}
}
