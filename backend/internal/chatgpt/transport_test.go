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

// signedInSource is a source that hands out a token without touching a network.
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

// sseBody is a Codex event stream. It is served without a Content-Type header
// on purpose: the real backend sends none, and a reader that branched on it
// would decide the body was JSON and fail on the first frame.
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

	// The Responses API has no system role: it becomes instructions, behind
	// the preamble the backend expects to lead them.
	if !strings.HasPrefix(sent.Instructions, codexInstructions) {
		t.Errorf("instructions = %q, want the Codex preamble first", sent.Instructions)
	}
	if !strings.Contains(sent.Instructions, "Return JSON.") {
		t.Errorf("instructions dropped the caller's system message: %q", sent.Instructions)
	}
	if len(sent.Input) != 1 || sent.Input[0].Role != "user" || sent.Input[0].Content[0].Type != "input_text" {
		t.Fatalf("input = %+v", sent.Input)
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
	// Usage is what the token accounting in ai.logUsage records; dropping it
	// would leave every ChatGPT completion logged as a line of zeros.
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

// The SDK's error decoding and ai.CompleteChat's retry-without-JSON-mode path
// both read the upstream body. Rewriting a failure would hide both.
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

// Anything that is not a chat completion -- a models listing, an embeddings
// call from a misbound provider -- must reach the network as it was.
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

// A stream that stops early still carries an answer worth keeping: the
// extractor parses leniently and the chatter shows what arrived.
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

// A provider row nobody signed in to must fail before it reaches the network,
// with a message that says what is missing.
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

func TestArrayContentIsFlattened(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`[{"type":"text","text":"one"},{"type":"text","text":"two"}]`)
	if got := messageText(raw); got != "one\ntwo" {
		t.Fatalf("messageText = %q", got)
	}
	if got := messageText(json.RawMessage(`"plain"`)); got != "plain" {
		t.Fatalf("messageText = %q", got)
	}
}
