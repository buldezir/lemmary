package chatgpt

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func signedInSource(t *testing.T, id string) *TokenSource {
	t.Helper()
	Forget(id)
	t.Cleanup(func() { Forget(id) })
	raw, err := Token{
		Access: "access-1", Refresh: "r", AccountID: "acct-9",
		ExpiresAt: time.Now().Add(time.Hour),
	}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return SourceFor(id, raw, nil, nil)
}

// sseBody is a Codex event stream, served without a Content-Type header
// because the real backend sends none.
func sseResponse(events ...string) *http.Response {
	var b strings.Builder
	for _, e := range events {
		b.WriteString("event: x\ndata: " + e + "\n\n")
	}
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{},
		Body:       io.NopCloser(strings.NewReader(b.String())),
	}
}

func chatBody(t *testing.T, stream bool) io.ReadCloser {
	t.Helper()
	body := map[string]any{
		"model":  "gpt-5.6-luna",
		"stream": stream,
		"messages": []map[string]any{
			{"role": "system", "content": "Return JSON."},
			{"role": "user", "content": "Who signed this?"},
		},
		"max_tokens":      int64(256),
		"response_format": map[string]any{"type": "json_object"},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return io.NopCloser(bytes.NewReader(raw))
}

func chatRequestFor(t *testing.T, stream bool) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://chatgpt.com/backend-api/codex/chat/completions", chatBody(t, stream))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func TestChatCompletionBecomesACodexResponsesCall(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t1"), nil)

	var sent responsesRequest
	var sentReq *http.Request
	_, err := mw(chatRequestFor(t, false), func(req *http.Request) (*http.Response, error) {
		sentReq = req
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatalf("the rewritten body is not a responses request: %v", err)
		}
		return sseResponse(
			`{"type":"response.output_text.delta","delta":"Ada "}`,
			`{"type":"response.output_text.delta","delta":"Lovelace"}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":11,"output_tokens":3,"input_tokens_details":{"cached_tokens":7}}}}`,
		), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if got := sentReq.URL.Path; got != "/backend-api/codex/responses" {
		t.Fatalf("path = %q, want the responses endpoint", got)
	}
	// Without these the backend answers 403 regardless of the token.
	for header, want := range map[string]string{
		"Authorization":      "Bearer access-1",
		"chatgpt-account-id": "acct-9",
		"OpenAI-Beta":        codexBeta,
		"originator":         codexOriginator,
	} {
		if got := sentReq.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if sentReq.Header.Get("session_id") == "" {
		t.Error("session_id is empty")
	}

	// The Responses API has no system role: it becomes instructions.
	if !strings.HasPrefix(sent.Instructions, codexInstructions) {
		t.Errorf("instructions = %q, want the Codex preamble first", sent.Instructions)
	}
	if !strings.Contains(sent.Instructions, "Return JSON.") {
		t.Errorf("instructions dropped the caller's system message: %q", sent.Instructions)
	}
	if len(sent.Input) != 2 || sent.Input[0].Role != "user" || sent.Input[0].Content[0].Type != "input_text" {
		t.Fatalf("input = %+v", sent.Input)
	}
	// The system message's "JSON" went into instructions, which the backend
	// does not scan, so JSON mode needs an input item that says it.
	if sent.Input[1].Role != "user" || !strings.Contains(strings.ToLower(sent.Input[1].Content[0].Text), "json") {
		t.Errorf("json nudge = %+v", sent.Input[1])
	}
	if !sent.Stream {
		t.Error("stream must be forced on: the endpoint answers no other way")
	}
	// Nobody asked for their archive to be kept in a ChatGPT history.
	if sent.Store {
		t.Error("store must be off")
	}
	if sent.MaxOutputTokens == nil || *sent.MaxOutputTokens != 256 {
		t.Errorf("max_output_tokens = %v, want the caller's max_tokens", sent.MaxOutputTokens)
	}
	if sent.Text == nil || sent.Text.Format.Type != "json_object" {
		t.Errorf("text.format = %+v, want the caller's response_format", sent.Text)
	}
}

func TestABufferedAnswerIsReassembledIntoAChatCompletion(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t2"), nil)

	resp, err := mw(chatRequestFor(t, false), func(req *http.Request) (*http.Response, error) {
		return sseResponse(
			`{"type":"response.output_text.delta","delta":"Ada "}`,
			`{"type":"response.output_text.delta","delta":"Lovelace"}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":11,"output_tokens":3,"total_tokens":14,"input_tokens_details":{"cached_tokens":7}}}}`,
		), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q", got)
	}

	var out chatCompletion
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "Ada Lovelace" {
		t.Fatalf("choices = %+v", out.Choices)
	}
	if out.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q", out.Choices[0].FinishReason)
	}
	// Usage is what ai.logUsage records; dropping it would log zeros.
	if out.Usage.PromptTokens != 11 || out.Usage.CompletionTokens != 3 {
		t.Fatalf("usage = %+v", out.Usage)
	}
	if out.Usage.PromptTokensDetails == nil || out.Usage.PromptTokensDetails.CachedTokens != 7 {
		t.Errorf("cached tokens = %+v", out.Usage.PromptTokensDetails)
	}
}

func TestAStreamedAnswerBecomesChatCompletionChunks(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t3"), nil)

	resp, err := mw(chatRequestFor(t, true), func(req *http.Request) (*http.Response, error) {
		return sseResponse(
			`{"type":"response.output_text.delta","delta":"Ada "}`,
			`{"type":"response.output_text.delta","delta":"Lovelace"}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":11,"output_tokens":3}}}`,
		), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q", got)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.HasSuffix(strings.TrimSpace(body), "data: [DONE]") {
		t.Errorf("stream does not end with the terminator: %q", body)
	}

	var text strings.Builder
	var sawStop bool
	var usage *chatUsage
	for _, line := range strings.Split(body, "\n") {
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if !strings.HasPrefix(line, "data:") || payload == "[DONE]" {
			continue
		}
		var chunk chatChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			t.Fatalf("chunk is not a chat.completion.chunk: %v", err)
		}
		if chunk.Object != "chat.completion.chunk" {
			t.Errorf("object = %q", chunk.Object)
		}
		for _, choice := range chunk.Choices {
			text.WriteString(choice.Delta.Content)
			if choice.FinishReason != nil && *choice.FinishReason == "stop" {
				sawStop = true
			}
		}
		if chunk.Usage != nil {
			usage = chunk.Usage
		}
	}
	if text.String() != "Ada Lovelace" {
		t.Fatalf("streamed text = %q", text.String())
	}
	if !sawStop {
		t.Error("no chunk carried finish_reason=stop")
	}
	// ai.completeStreaming reads usage off the final chunk and nowhere else.
	if usage == nil || usage.PromptTokens != 11 {
		t.Fatalf("usage = %+v", usage)
	}
}

// The SDK's error decoding and ai.CompleteChat's retry path both read the
// upstream body.
func TestAnErrorResponsePassesThroughUntouched(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t4"), nil)

	resp, err := mw(chatRequestFor(t, false), func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 400,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"nope"}}`)),
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), "nope") {
		t.Fatalf("body was rewritten: %s", raw)
	}
}

// Anything that is not a chat completion must reach the network as it was.
func TestOtherPathsAreNotRewritten(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t5"), nil)

	req, err := http.NewRequest(http.MethodGet, "https://chatgpt.com/backend-api/codex/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := mw(req, func(got *http.Request) (*http.Response, error) {
		called = true
		if got.URL.Path != "/backend-api/codex/models" {
			t.Errorf("path = %q, want it untouched", got.URL.Path)
		}
		if got.Header.Get("Authorization") != "" {
			t.Error("a passed-through request was given a token")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("the request never reached the transport")
	}
}

// A stream that stops early still carries an answer worth keeping.
func TestAPartialAnswerSurvivesAFailedStream(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t6"), nil)

	resp, err := mw(chatRequestFor(t, false), func(req *http.Request) (*http.Response, error) {
		return sseResponse(
			`{"type":"response.output_text.delta","delta":"half an "}`,
			`{"type":"response.failed","response":{"error":{"message":"rate limit"}}}`,
		), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out chatCompletion
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Choices[0].Message.Content != "half an " {
		t.Fatalf("content = %q", out.Choices[0].Message.Content)
	}

	// A stream that produced nothing at all is a failure, not an empty reply.
	if _, err := mw(chatRequestFor(t, false), func(req *http.Request) (*http.Response, error) {
		return sseResponse(`{"type":"response.failed","response":{"error":{"message":"rate limit"}}}`), nil
	}); err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatalf("err = %v, want the backend's reason", err)
	}
}

func TestAnUnsignedProviderNeverReachesTheNetwork(t *testing.T) {
	t.Parallel()
	Forget("t7")
	defer Forget("t7")
	mw := Middleware(SourceFor("t7", "", nil, nil), nil)

	if _, err := mw(chatRequestFor(t, false), func(req *http.Request) (*http.Response, error) {
		t.Fatal("an unsigned provider reached the transport")
		return nil, nil
	}); err != ErrNotSignedIn {
		t.Fatalf("err = %v, want ErrNotSignedIn", err)
	}
}

// instructions is a string, so an attachment in a system message has nowhere
// to go and text is all that is kept.
func TestArrayContentIsFlattened(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`)
	if got := messageText(raw); got != "one\ntwo" {
		t.Fatalf("messageText = %q", got)
	}
	if got := messageText(json.RawMessage(`"plain"`)); got != "plain" {
		t.Fatalf("messageText = %q", got)
	}
	withImage := json.RawMessage(`[{"type":"text","text":"read this"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA"}}]`)
	if got := messageText(withImage); got != "read this" {
		t.Fatalf("messageText = %q, want the text alone", got)
	}
}

// searchToolBody is a tool-bearing completion in the shape openai-go marshals
// one, with tool_choice as the bare string the search and research loops send.
func searchToolBody(t *testing.T, choice string, messages []map[string]any) io.ReadCloser {
	t.Helper()
	if messages == nil {
		messages = []map[string]any{
			{"role": "system", "content": "You search an archive."},
			{"role": "user", "content": "What did the landlord send in May?"},
		}
	}
	raw, err := json.Marshal(map[string]any{
		"model":    "gpt-5.6-luna",
		"messages": messages,
		"tools": []map[string]any{{
			"type": "function",
			"function": map[string]any{
				"name":        "search_documents",
				"description": "Search the user's document archive.",
				"parameters": map[string]any{
					"type":       "object",
					"properties": map[string]any{"query": map[string]any{"type": "string"}},
					"required":   []string{"query"},
				},
			},
		}},
		"tool_choice": choice,
	})
	if err != nil {
		t.Fatal(err)
	}
	return io.NopCloser(bytes.NewReader(raw))
}

func toolRequestFor(t *testing.T, choice string, messages []map[string]any) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://chatgpt.com/backend-api/codex/chat/completions", searchToolBody(t, choice, messages))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// The search loop reads a round with no tool calls as a finished answer, so a
// middleware that dropped the tools fails as a confident reply with no
// documents behind it, not as an error.
func TestFunctionToolsReachTheCodexEndpoint(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t10"), nil)

	var sent responsesRequest
	_, err := mw(toolRequestFor(t, "auto", nil), func(req *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatal(err)
		}
		return sseResponse(`{"type":"response.completed","response":{"status":"completed"}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(sent.Tools) != 1 {
		t.Fatalf("tools = %+v, want the caller's one function", sent.Tools)
	}
	tool := sent.Tools[0]
	if tool.Type != "function" || tool.Name != "search_documents" {
		t.Errorf("tool = %+v", tool)
	}
	if !strings.Contains(tool.Description, "document archive") {
		t.Errorf("description = %q", tool.Description)
	}
	if !strings.Contains(string(tool.Parameters), `"query"`) {
		t.Errorf("parameters = %s", tool.Parameters)
	}
	if tool.Strict {
		t.Error("strict must stay off: the archive's schemas are guidance, and strict mode rejects several")
	}
	if got := strings.TrimSpace(string(sent.ToolChoice)); got != `"auto"` {
		t.Errorf("tool_choice = %s, want the caller's", got)
	}
}

// An endpoint that sees a bare tool_choice with no tools array answers 400.
func TestToolChoiceNoneIsCarriedWithTheTools(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t11"), nil)

	var sent responsesRequest
	_, err := mw(toolRequestFor(t, "none", nil), func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatal(err)
		}
		return sseResponse(`{"type":"response.completed","response":{"status":"completed"}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.Tools) != 1 {
		t.Fatalf("tools = %+v", sent.Tools)
	}
	if got := strings.TrimSpace(string(sent.ToolChoice)); got != `"none"` {
		t.Errorf("tool_choice = %s", got)
	}
}

// The field is meaningless without tools and some endpoints refuse it.
func TestAToollessCompletionSendsNoToolChoice(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t12"), nil)

	var sent responsesRequest
	_, err := mw(chatRequestFor(t, false), func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatal(err)
		}
		return sseResponse(`{"type":"response.completed","response":{"status":"completed"}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(sent.Tools) != 0 || len(sent.ToolChoice) != 0 {
		t.Errorf("tools = %+v, tool_choice = %s", sent.Tools, sent.ToolChoice)
	}
}

// Chat carries tool calls on the assistant message and answers them with a
// tool-role message, where Responses makes each one a free-standing item.
func TestAToolRoundReplaysAsFreeStandingItems(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t13"), nil)

	messages := []map[string]any{
		{"role": "system", "content": "You search an archive."},
		{"role": "user", "content": "What did the landlord send in May?"},
		{
			"role":    "assistant",
			"content": nil,
			"tool_calls": []map[string]any{{
				"id":       "call_7",
				"type":     "function",
				"function": map[string]any{"name": "search_documents", "arguments": `{"query":"landlord"}`},
			}},
		},
		{"role": "tool", "tool_call_id": "call_7", "content": "1 hit: rent increase notice"},
	}

	var sent responsesRequest
	_, err := mw(toolRequestFor(t, "auto", messages), func(req *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(req.Body)
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatal(err)
		}
		return sseResponse(`{"type":"response.completed","response":{"status":"completed"}}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(sent.Input) != 3 {
		t.Fatalf("input = %+v, want the user turn, the call and its result", sent.Input)
	}
	// An assistant message with tool calls and no content must not be dropped
	// by the empty-content skip.
	call := sent.Input[1]
	if call.Type != "function_call" || call.CallID != "call_7" || call.Name != "search_documents" {
		t.Fatalf("call item = %+v", call)
	}
	if call.Arguments != `{"query":"landlord"}` {
		t.Errorf("arguments = %q", call.Arguments)
	}
	if len(call.Content) != 0 || call.Role != "" {
		t.Errorf("a function_call must carry no role or content: %+v", call)
	}
	result := sent.Input[2]
	if result.Type != "function_call_output" || result.CallID != "call_7" {
		t.Fatalf("result item = %+v", result)
	}
	if result.Output != "1 hit: rent increase notice" {
		t.Errorf("output = %q", result.Output)
	}
}

// A function_call the model produced has to come back as message.tool_calls,
// the only thing the search and research loops look at.
func TestAFunctionCallComesBackAsAToolCall(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t14"), nil)

	resp, err := mw(toolRequestFor(t, "auto", nil), func(req *http.Request) (*http.Response, error) {
		return sseResponse(
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_7","name":"search_documents","arguments":"{\"query\":\"landlord\"}"}}`,
			`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":40,"output_tokens":9}}}`,
		), nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var out chatCompletion
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) != 1 {
		t.Fatalf("choices = %+v", out.Choices)
	}
	calls := out.Choices[0].Message.ToolCalls
	if len(calls) != 1 {
		t.Fatalf("tool_calls = %+v", calls)
	}
	if calls[0].ID != "call_7" || calls[0].Type != "function" || calls[0].Function.Name != "search_documents" {
		t.Fatalf("call = %+v", calls[0])
	}
	if calls[0].Function.Arguments != `{"query":"landlord"}` {
		t.Errorf("arguments = %q", calls[0].Function.Arguments)
	}
	// A model that asked for a tool has not finished answering.
	if out.Choices[0].FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %q", out.Choices[0].FinishReason)
	}
}

// Some backends report their output only on the completed response.
func TestAFunctionCallOnTheCompletedResponseIsRead(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t15"), nil)

	resp, err := mw(toolRequestFor(t, "auto", nil), func(req *http.Request) (*http.Response, error) {
		return sseResponse(
			`{"type":"response.completed","response":{"status":"completed","output":[{"type":"message"},{"type":"function_call","call_id":"call_9","name":"search_documents","arguments":""}]}}`,
		), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out chatCompletion
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	calls := out.Choices[0].Message.ToolCalls
	if len(calls) != 1 || calls[0].ID != "call_9" {
		t.Fatalf("tool_calls = %+v", calls)
	}
	if calls[0].Function.Arguments != "{}" {
		t.Errorf("arguments = %q, want an empty object", calls[0].Function.Arguments)
	}
}

func TestAnItemEventAndACompletedResponseDoNotDoubleCount(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t16"), nil)

	resp, err := mw(toolRequestFor(t, "auto", nil), func(req *http.Request) (*http.Response, error) {
		return sseResponse(
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_7","name":"search_documents","arguments":"{}"}}`,
			`{"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","call_id":"call_7","name":"search_documents","arguments":"{}"}]}}`,
		), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out chatCompletion
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if calls := out.Choices[0].Message.ToolCalls; len(calls) != 1 {
		t.Fatalf("tool_calls = %+v, want one", calls)
	}
}

func TestAStreamedFunctionCallBecomesAToolCallChunk(t *testing.T) {
	t.Parallel()
	mw := Middleware(signedInSource(t, "t17"), nil)

	req := toolRequestFor(t, "auto", nil)
	// Rebuild the body with stream on: the caller's flag is what picks the path.
	var body map[string]any
	raw, _ := io.ReadAll(req.Body)
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	body["stream"] = true
	reraw, _ := json.Marshal(body)
	req.Body = io.NopCloser(bytes.NewReader(reraw))

	resp, err := mw(req, func(*http.Request) (*http.Response, error) {
		return sseResponse(
			`{"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_7","name":"search_documents","arguments":"{\"query\":\"rent\"}"}}`,
			`{"type":"response.completed","response":{"status":"completed"}}`,
		), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	if !strings.Contains(text, `"tool_calls"`) || !strings.Contains(text, "call_7") {
		t.Fatalf("stream carried no tool call:\n%s", text)
	}
	if !strings.Contains(text, `"finish_reason":"tool_calls"`) {
		t.Errorf("final chunk did not finish on tool_calls:\n%s", text)
	}
}

// ocrRequestFor is the request internal/ocr builds: a system message, then a
// prompt plus the document as an image_url or file part carrying a base64 data
// URI. See ocr.LLMUserContentParts.
func ocrRequestFor(t *testing.T, document map[string]any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"model": "gpt-5.6-luna",
		"messages": []map[string]any{
			{"role": "system", "content": "You transcribe documents for an archive."},
			{"role": "user", "content": []map[string]any{
				{"type": "text", "text": "Extract all text from this document."},
				document,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://chatgpt.com/backend-api/codex/chat/completions", io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return req
}

func sentInputFor(t *testing.T, mw func(*http.Request, func(*http.Request) (*http.Response, error)) (*http.Response, error), req *http.Request) responsesRequest {
	t.Helper()
	var sent responsesRequest
	if _, err := mw(req, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &sent); err != nil {
			t.Fatal(err)
		}
		return sseResponse(`{"type":"response.output_text.delta","delta":"text"}`,
			`{"type":"response.completed","response":{"status":"completed"}}`), nil
	}); err != nil {
		t.Fatal(err)
	}
	return sent
}

// A middleware that kept only the text parts would send the model a
// transcription prompt with no document behind it, and the model would answer:
// an invented transcription rather than an error.
func TestAScanReachesTheModelAsAnImage(t *testing.T) {
	t.Parallel()
	const dataURI = "data:image/png;base64,iVBORw0KGgo="
	sent := sentInputFor(t, Middleware(signedInSource(t, "t20"), nil), ocrRequestFor(t, map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": dataURI},
	}))

	if len(sent.Input) != 1 {
		t.Fatalf("input = %+v, want the one user message", sent.Input)
	}
	parts := sent.Input[0].Content
	if len(parts) != 2 {
		t.Fatalf("content = %+v, want the prompt and the image", parts)
	}
	if parts[0].Type != "input_text" || !strings.Contains(parts[0].Text, "Extract all text") {
		t.Errorf("prompt part = %+v", parts[0])
	}
	// input_image carries the URL on the part itself; the nested chat shape
	// earns an opaque 400.
	if parts[1].Type != "input_image" || parts[1].ImageURL != dataURI {
		t.Fatalf("image part = %+v", parts[1])
	}
}

func TestAPDFReachesTheModelAsAFile(t *testing.T) {
	t.Parallel()
	const dataURI = "data:application/pdf;base64,JVBERi0="
	sent := sentInputFor(t, Middleware(signedInSource(t, "t21"), nil), ocrRequestFor(t, map[string]any{
		"type": "file",
		"file": map[string]any{"filename": "rent.pdf", "file_data": dataURI},
	}))

	parts := sent.Input[0].Content
	if len(parts) != 2 {
		t.Fatalf("content = %+v, want the prompt and the file", parts)
	}
	if parts[1].Type != "input_file" || parts[1].FileData != dataURI {
		t.Fatalf("file part = %+v", parts[1])
	}
	// The name is what tells the model it is looking at a PDF.
	if parts[1].Filename != "rent.pdf" {
		t.Errorf("filename = %q", parts[1].Filename)
	}
}

// LLMUserContentParts defaults the name, but a caller reaching this
// middleware directly may not.
func TestAnUnnamedFileStillGetsAName(t *testing.T) {
	t.Parallel()
	sent := sentInputFor(t, Middleware(signedInSource(t, "t22"), nil), ocrRequestFor(t, map[string]any{
		"type": "file",
		"file": map[string]any{"file_data": "data:application/pdf;base64,JVBERi0="},
	}))
	parts := sent.Input[0].Content
	if len(parts) != 2 || parts[1].Filename == "" {
		t.Fatalf("content = %+v, want a named file part", parts)
	}
}

// An unknown shape forwarded to the backend earns an opaque 400.
func TestAnUnknownContentPartIsDropped(t *testing.T) {
	t.Parallel()
	sent := sentInputFor(t, Middleware(signedInSource(t, "t23"), nil), ocrRequestFor(t, map[string]any{
		"type":        "input_audio",
		"input_audio": map[string]any{"data": "AAA", "format": "wav"},
	}))
	parts := sent.Input[0].Content
	if len(parts) != 1 || parts[0].Type != "input_text" {
		t.Fatalf("content = %+v, want only the text part", parts)
	}
}

// An assistant turn's text is output_text, not input_text: a replayed
// conversation that got it wrong would be refused.
func TestAssistantTextIsOutputText(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]any{
		"model": "gpt-5.6-luna",
		"messages": []map[string]any{
			{"role": "user", "content": "hello"},
			{"role": "assistant", "content": "hi"},
			{"role": "user", "content": "again"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://chatgpt.com/backend-api/codex/chat/completions", io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	sent := sentInputFor(t, Middleware(signedInSource(t, "t24"), nil), req)

	if len(sent.Input) != 3 {
		t.Fatalf("input = %+v", sent.Input)
	}
	if sent.Input[0].Content[0].Type != "input_text" {
		t.Errorf("user part = %+v", sent.Input[0].Content[0])
	}
	if sent.Input[1].Role != "assistant" || sent.Input[1].Content[0].Type != "output_text" {
		t.Errorf("assistant part = %+v", sent.Input[1])
	}
}

func TestAUserMessageThatSaysJSONGetsNoNudge(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(map[string]any{
		"model": "gpt-5.6-luna",
		"messages": []map[string]any{
			{"role": "system", "content": "You extract fields."},
			{"role": "user", "content": "Answer as JSON: who signed this?"},
		},
		"response_format": map[string]any{"type": "json_object"},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://chatgpt.com/backend-api/codex/chat/completions", io.NopCloser(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	sent := sentInputFor(t, Middleware(signedInSource(t, "t25"), nil), req)

	if len(sent.Input) != 1 {
		t.Fatalf("input = %+v", sent.Input)
	}
}
