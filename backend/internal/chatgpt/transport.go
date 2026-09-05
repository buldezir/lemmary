package chatgpt

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/openai/openai-go/option"
	"lemmary/backend/internal/aiprovider"
)

const (
	// codexOriginator names us to the Codex backend, which serves only its own
	// clients: the header is checked against a whitelist and anything else is
	// refused with a 403. It is also sent to the auth host, which is equally
	// particular.
	codexOriginator = "codex_cli_rs"

	// codexBeta is the opt-in the Responses endpoint requires.
	codexBeta = "responses=experimental"

	// codexInstructions is the preamble the backend expects to lead the
	// instructions field.
	//
	// It is short on purpose. The backend wants to see a Codex-shaped system
	// prompt, and the real caller's system message follows immediately after,
	// so this only has to satisfy the shape without steering the model away
	// from the job it was actually given. If a future backend change starts
	// rejecting requests, this is the first knob to turn.
	codexInstructions = "You are a coding agent running in a terminal."
)

// PlaceholderKey stands in for the API key openai-go insists on having.
//
// A chatgpt client has no such key: Middleware sets a live bearer token on
// every request instead. The value still has to be non-empty, both for the SDK
// and for the `apiKey == ""` guards the chatter, splitter, helper and search
// agent open with -- and it is never sent, because applyCodexHeaders overwrites
// the Authorization header the SDK built from it.
const PlaceholderKey = "chatgpt-oauth"

// chatCompletionsSuffix is the path openai-go builds from any base URL. Matching
// on it rather than on the whole URL is what lets the middleware sit under a
// client whose base URL a test has repointed at httptest.
const chatCompletionsSuffix = "/chat/completions"

// Middleware makes the Codex backend answer Chat Completions requests.
//
// It is installed in place of an API key: the SDK is built with a placeholder
// credential and this overwrites the Authorization header with a live token on
// every attempt. Requests to any other path pass through untouched, which is
// what keeps a misbound provider from silently rewriting somebody else's call.
func Middleware(src *TokenSource, logger *slog.Logger) option.Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if req == nil || req.URL == nil || !strings.HasSuffix(req.URL.Path, chatCompletionsSuffix) {
			return next(req)
		}

		in, err := readChatRequest(req)
		if err != nil {
			return nil, err
		}
		tok, err := src.AccessToken(req.Context())
		if err != nil {
			return nil, err
		}

		payload, err := json.Marshal(toResponsesRequest(in))
		if err != nil {
			return nil, fmt.Errorf("chatgpt: encode responses request: %w", err)
		}
		req.URL.Path = strings.TrimSuffix(req.URL.Path, chatCompletionsSuffix) + "/responses"
		req.Body = io.NopCloser(bytes.NewReader(payload))
		req.ContentLength = int64(len(payload))
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(payload)), nil
		}
		applyCodexHeaders(req, tok)

		resp, err := next(req)
		if err != nil || resp == nil {
			return resp, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			// Left exactly as it arrived: the SDK's own error decoding turns it
			// into an APIError, and ai.CompleteChat's degradation paths read
			// that body to decide whether to retry without JSON mode.
			return resp, nil
		}
		if in.Stream {
			return streamingResponse(resp, in.Model), nil
		}
		return bufferedResponse(resp, in.Model, logger)
	}
}

func applyCodexHeaders(req *http.Request, tok Token) {
	req.Header.Set("Authorization", "Bearer "+tok.Access)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("OpenAI-Beta", codexBeta)
	req.Header.Set("originator", codexOriginator)
	if tok.AccountID != "" {
		req.Header.Set("chatgpt-account-id", tok.AccountID)
	}
	// The same per-purpose id the OpenCode header carries elsewhere: stable for
	// the life of the process, so requests sharing a system prompt stay
	// grouped. Falls back rather than sending nothing, which the backend
	// dislikes.
	session := aiprovider.SessionFrom(req.Context())
	if session == "" {
		session = aiprovider.SessionFor("chatgpt")
	}
	req.Header.Set("session_id", session)
}

// --- request translation ---

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Stream              bool            `json:"stream"`
	MaxTokens           *int64          `json:"max_tokens"`
	MaxCompletionTokens *int64          `json:"max_completion_tokens"`
	ResponseFormat      *responseFormat `json:"response_format"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type responsesRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions,omitempty"`
	Input           []responsesItem `json:"input"`
	Stream          bool            `json:"stream"`
	Store           bool            `json:"store"`
	MaxOutputTokens *int64          `json:"max_output_tokens,omitempty"`
	Text            *responsesText  `json:"text,omitempty"`
}

type responsesItem struct {
	Type    string             `json:"type"`
	Role    string             `json:"role"`
	Content []responsesContent `json:"content"`
}

type responsesContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type responsesText struct {
	Format responseFormat `json:"format"`
}

func readChatRequest(req *http.Request) (chatRequest, error) {
	var in chatRequest
	if req.Body == nil {
		return in, fmt.Errorf("chatgpt: chat completion request has no body")
	}
	raw, err := io.ReadAll(req.Body)
	_ = req.Body.Close()
	if err != nil {
		return in, fmt.Errorf("chatgpt: read chat completion request: %w", err)
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return in, fmt.Errorf("chatgpt: decode chat completion request: %w", err)
	}
	return in, nil
}

// toResponsesRequest maps a chat completion onto the Responses shape.
//
// Two things are forced rather than carried over. stream is always true because
// the Codex endpoint answers no other way -- a non-streaming caller gets the
// stream reassembled below. store is always false: this is somebody's document
// archive, and leaving copies of it in a ChatGPT history is not something an
// operator asked for by signing in.
//
// temperature is dropped by having nowhere to go: the Responses shape here
// carries no such field, so whatever ai.CompletionTemperature decided upstream
// never reaches the backend. The Codex models refuse a custom value anyway.
func toResponsesRequest(in chatRequest) responsesRequest {
	out := responsesRequest{
		Model:  in.Model,
		Stream: true,
		Store:  false,
		Input:  make([]responsesItem, 0, len(in.Messages)),
	}

	instructions := []string{codexInstructions}
	for _, msg := range in.Messages {
		text := messageText(msg.Content)
		if strings.TrimSpace(text) == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(msg.Role)) {
		case "system", "developer":
			// The Responses API has no system message: the role's content is
			// the instructions field, which is also where the backend looks
			// for the preamble above.
			instructions = append(instructions, text)
		case "assistant":
			out.Input = append(out.Input, responsesItem{
				Type: "message", Role: "assistant",
				Content: []responsesContent{{Type: "output_text", Text: text}},
			})
		default:
			out.Input = append(out.Input, responsesItem{
				Type: "message", Role: "user",
				Content: []responsesContent{{Type: "input_text", Text: text}},
			})
		}
	}
	out.Instructions = strings.Join(instructions, "\n\n")

	switch {
	case in.MaxCompletionTokens != nil:
		out.MaxOutputTokens = in.MaxCompletionTokens
	case in.MaxTokens != nil:
		out.MaxOutputTokens = in.MaxTokens
	}

	if in.ResponseFormat != nil && strings.TrimSpace(in.ResponseFormat.Type) != "" {
		out.Text = &responsesText{Format: responseFormat{Type: in.ResponseFormat.Type}}
	}
	return out
}

// messageText flattens a chat message's content.
//
// Content is either a plain string or an array of typed parts. Only the text
// parts are kept: the one caller that sends images is OCR, and CanOCR already
// refuses this SDK, so an image here means a misconfiguration rather than a
// case to support.
func messageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		if part.Text == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(part.Text)
	}
	return b.String()
}

// --- response translation ---

type chatUsage struct {
	PromptTokens        int            `json:"prompt_tokens"`
	CompletionTokens    int            `json:"completion_tokens"`
	TotalTokens         int            `json:"total_tokens"`
	PromptTokensDetails *promptDetails `json:"prompt_tokens_details,omitempty"`
}

type promptDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type chatCompletion struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Created int64        `json:"created"`
	Model   string       `json:"model"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Index        int            `json:"index"`
	Message      chatOutMessage `json:"message"`
	FinishReason string         `json:"finish_reason"`
}

type chatOutMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chatUsage    `json:"usage,omitempty"`
}

type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

type chunkDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// responsesEvent is the subset of the Codex SSE stream that carries an answer.
type responsesEvent struct {
	Type     string `json:"type"`
	Delta    string `json:"delta"`
	Response *struct {
		Status string `json:"status"`
		Usage  *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			InputTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	} `json:"response"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e responsesEvent) usage() chatUsage {
	var out chatUsage
	if e.Response == nil || e.Response.Usage == nil {
		return out
	}
	u := e.Response.Usage
	out.PromptTokens = u.InputTokens
	out.CompletionTokens = u.OutputTokens
	out.TotalTokens = u.TotalTokens
	if out.TotalTokens == 0 {
		out.TotalTokens = u.InputTokens + u.OutputTokens
	}
	if u.InputTokensDetails != nil {
		out.PromptTokensDetails = &promptDetails{CachedTokens: u.InputTokensDetails.CachedTokens}
	}
	return out
}

func (e responsesEvent) failure() error {
	if e.Error != nil && strings.TrimSpace(e.Error.Message) != "" {
		return fmt.Errorf("chatgpt: %s", e.Error.Message)
	}
	if e.Response != nil && e.Response.Error != nil && strings.TrimSpace(e.Response.Error.Message) != "" {
		return fmt.Errorf("chatgpt: %s", e.Response.Error.Message)
	}
	return fmt.Errorf("chatgpt: the model did not finish its answer")
}

// bufferedResponse reassembles the whole stream into one chat completion, for
// the callers that asked for a plain response.
func bufferedResponse(resp *http.Response, model string, logger *slog.Logger) (*http.Response, error) {
	defer resp.Body.Close()

	var text strings.Builder
	var usage chatUsage
	var failure error
	err := scanSSE(resp.Body, func(event responsesEvent) error {
		switch event.Type {
		case "response.output_text.delta":
			text.WriteString(event.Delta)
		case "response.completed":
			usage = event.usage()
		case "response.failed", "response.incomplete", "error":
			failure = event.failure()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if failure != nil {
		// A partial answer is worth more than none to every caller here -- the
		// extractor parses leniently and the chatter shows what it got -- but a
		// stream that produced nothing at all is a failure, not an empty reply.
		if text.Len() == 0 {
			return nil, failure
		}
		logger.Warn("the ChatGPT response ended early; keeping the partial answer",
			"model", model, slog.Any("error", failure))
	}

	body, err := json.Marshal(chatCompletion{
		ID:      completionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chatChoice{{
			Index:        0,
			Message:      chatOutMessage{Role: "assistant", Content: text.String()},
			FinishReason: "stop",
		}},
		Usage: usage,
	})
	if err != nil {
		return nil, fmt.Errorf("chatgpt: encode chat completion: %w", err)
	}

	out := cloneResponseHead(resp)
	out.Header.Set("Content-Type", "application/json")
	out.Header.Set("Content-Length", strconv.Itoa(len(body)))
	out.Body = io.NopCloser(bytes.NewReader(body))
	out.ContentLength = int64(len(body))
	return out, nil
}

// streamingResponse re-emits the Codex stream as chat completion chunks, so the
// SDK's own stream decoder -- and ai.completeStreaming above it -- see the
// shape they expect.
func streamingResponse(resp *http.Response, model string) *http.Response {
	pr, pw := io.Pipe()

	// Cloned before the goroutine starts, so nothing reads the upstream
	// response's fields while the goroutine is draining its body.
	out := cloneResponseHead(resp)
	out.Header.Set("Content-Type", "text/event-stream")
	out.Header.Del("Content-Length")
	out.Body = pr
	out.ContentLength = -1

	go func() {
		defer resp.Body.Close()

		id := completionID()
		created := time.Now().Unix()
		writeChunk := func(chunk chatChunk) error {
			payload, err := json.Marshal(chunk)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(pw, "data: %s\n\n", payload)
			return err
		}

		var usage chatUsage
		var failure error
		err := scanSSE(resp.Body, func(event responsesEvent) error {
			switch event.Type {
			case "response.output_text.delta":
				if event.Delta == "" {
					return nil
				}
				return writeChunk(chatChunk{
					ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
					Choices: []chunkChoice{{Delta: chunkDelta{Content: event.Delta}}},
				})
			case "response.completed":
				usage = event.usage()
			case "response.failed", "response.incomplete", "error":
				failure = event.failure()
			}
			return nil
		})
		if err == nil && failure != nil {
			err = failure
		}
		if err != nil {
			pw.CloseWithError(err)
			return
		}

		stop := "stop"
		final := chatChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []chunkChoice{{Delta: chunkDelta{}, FinishReason: &stop}},
		}
		// Usage rides the final chunk, which is where stream_options puts it and
		// where ai.completeStreaming looks.
		if usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
			final.Usage = &usage
		}
		if err := writeChunk(final); err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.WriteString(pw, "data: [DONE]\n\n"); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.Close()
	}()

	return out
}

// cloneResponseHead copies everything but the body, so the SDK still sees the
// upstream status and request id.
func cloneResponseHead(resp *http.Response) *http.Response {
	out := *resp
	out.Header = resp.Header.Clone()
	if out.Header == nil {
		out.Header = http.Header{}
	}
	return &out
}

// scanSSE reads an event stream.
//
// Content-Type is never consulted: the Codex backend sends the stream without
// one, and a reader that branched on it would decide the body was JSON and fail
// on the first `data:`.
func scanSSE(r io.Reader, onEvent func(responsesEvent) error) error {
	scanner := bufio.NewScanner(r)
	// Deltas are small, but a single completed event carries the whole response
	// object. 1 MiB is well past anything observed and still bounded.
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			// event: lines and blank separators. The type is inside the JSON
			// too, so nothing is lost by ignoring the event: field.
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var event responsesEvent
		if err := json.Unmarshal([]byte(payload), &event); err != nil {
			// One unreadable frame is not worth abandoning a good answer for.
			continue
		}
		if err := onEvent(event); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("chatgpt: read response stream: %w", err)
	}
	return nil
}

func completionID() string {
	return "chatcmpl-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
