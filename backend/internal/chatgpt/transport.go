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
	// codexOriginator names us to the Codex backend, which checks the header
	// against a whitelist and refuses anything else with a 403. The auth host is
	// equally particular.
	codexOriginator = "codex_cli_rs"

	// codexBeta is the opt-in the Responses endpoint requires.
	codexBeta = "responses=experimental"

	// codexInstructions is the preamble the backend expects to lead the
	// instructions field. Short on purpose: the backend wants a Codex-shaped
	// system prompt, and the real caller's system message follows immediately
	// after. First knob to turn if the backend starts rejecting requests.
	codexInstructions = "You are a coding agent running in a terminal."
)

// PlaceholderKey stands in for the API key openai-go insists on having. A
// chatgpt client has no such key: Middleware sets a live bearer token on every
// request instead. It still has to be non-empty for the SDK and for the
// `apiKey == ""` guards the call sites open with, and it is never sent.
const PlaceholderKey = "chatgpt-oauth"

// chatCompletionsSuffix is the path openai-go builds from any base URL.
// Matching on it rather than the whole URL lets the middleware sit under a
// client whose base URL a test has repointed at httptest.
// on it rather than on the whole URL is what lets the middleware sit under a
// client whose base URL a test has repointed at httptest.
const chatCompletionsSuffix = "/chat/completions"

// Middleware makes the Codex backend answer Chat Completions requests, in
// place of an API key. Requests to any other path pass through untouched, which
// keeps a misbound provider from rewriting somebody else's call.
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
			// Left exactly as it arrived: ai.CompleteChat's degradation paths read
			// the error body to decide whether to retry without JSON mode.
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
	// The same per-purpose id the OpenCode header carries elsewhere, so requests
	// sharing a system prompt stay grouped. Falls back rather than sending
	// nothing, which the backend dislikes.
	session := aiprovider.SessionFrom(req.Context())
	if session == "" {
		session = aiprovider.SessionFor("chatgpt")
	}
	req.Header.Set("session_id", session)
}

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Stream              bool            `json:"stream"`
	MaxTokens           *int64          `json:"max_tokens"`
	MaxCompletionTokens *int64          `json:"max_completion_tokens"`
	ResponseFormat      *responseFormat `json:"response_format"`
	Tools               []chatTool      `json:"tools"`
	// ToolChoice is carried through as it arrived: both shapes the SDK can send
	// are also what the Responses endpoint accepts.
	ToolChoice json.RawMessage `json:"tool_choice"`
}

type responseFormat struct {
	Type string `json:"type"`
}

// chatTool is one function tool as Chat Completions declares it, nested under
// a "function" object.
type chatTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
		Strict      *bool           `json:"strict"`
	} `json:"function"`
}

type chatMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
	// ToolCalls is what an assistant message carries instead of content when
	// the model asked for a tool. Content is null on those, which is why the
	// empty-text skip below cannot come first.
	ToolCalls []chatToolCall `json:"tool_calls"`
	// ToolCallID ties a tool-role message back to the call it answers.
	ToolCallID string `json:"tool_call_id"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolCallFunc `json:"function"`
}

type chatToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type responsesRequest struct {
	Model           string          `json:"model"`
	Instructions    string          `json:"instructions,omitempty"`
	Input           []responsesItem `json:"input"`
	Stream          bool            `json:"stream"`
	Store           bool            `json:"store"`
	MaxOutputTokens *int64          `json:"max_output_tokens,omitempty"`
	Text            *responsesText  `json:"text,omitempty"`
	Tools           []responsesTool `json:"tools,omitempty"`
	ToolChoice      json.RawMessage `json:"tool_choice,omitempty"`
}

// responsesTool is the same function tool, flattened: the Responses shape puts
// the name and schema on the tool itself rather than under a "function" key.
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict"`
}

// responsesItem is one input item, and the Responses input list is a union: a
// message carries Role and Content, a function_call carries CallID/Name/
// Arguments, a function_call_output carries CallID and Output.
type responsesItem struct {
	Type      string             `json:"type"`
	Role      string             `json:"role,omitempty"`
	Content   []responsesContent `json:"content,omitempty"`
	CallID    string             `json:"call_id,omitempty"`
	Name      string             `json:"name,omitempty"`
	Arguments string             `json:"arguments,omitempty"`
	Output    string             `json:"output,omitempty"`
}

// responsesContent is one part of a message's content. The Responses shape
// puts an image's URL directly on the part, where Chat Completions nests it
// under an "image_url" object; a PDF uses Filename with FileData.
type responsesContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Filename string `json:"filename,omitempty"`
	FileData string `json:"file_data,omitempty"`
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

// toResponsesRequest maps a chat completion onto the Responses shape. stream
// is always true because the Codex endpoint answers no other way, and store
// always false: leaving copies of somebody's archive in a ChatGPT history is
// not what signing in asked for. temperature has nowhere to go here, and the
// Codex models refuse a custom value anyway.
func toResponsesRequest(in chatRequest) responsesRequest {
	out := responsesRequest{
		Model:  in.Model,
		Stream: true,
		Store:  false,
		Input:  make([]responsesItem, 0, len(in.Messages)),
	}

	instructions := []string{codexInstructions}
	for _, msg := range in.Messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))

		// The two shapes that are not a message come first: a tool result is an
		// item of its own, and an assistant message that only asked for a tool
		// has no content at all.
		if role == "tool" {
			if id := strings.TrimSpace(msg.ToolCallID); id != "" {
				out.Input = append(out.Input, responsesItem{
					Type: "function_call_output", CallID: id, Output: messageText(msg.Content),
				})
			}
			continue
		}
		if role == "assistant" && len(msg.ToolCalls) > 0 {
			// Some models say something before calling a tool. Keep it: it is
			// part of the conversation the next round replays.
			if content := messageContent(msg.Content, "assistant"); len(content) > 0 {
				out.Input = append(out.Input, responsesItem{
					Type: "message", Role: "assistant", Content: content,
				})
			}
			for _, call := range msg.ToolCalls {
				out.Input = append(out.Input, responsesItem{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				})
			}
			continue
		}

		// The Responses API has no system message: the role's content is the
		// instructions field, where the backend also looks for the preamble.
		if role == "system" || role == "developer" {
			if text := messageText(msg.Content); strings.TrimSpace(text) != "" {
				instructions = append(instructions, text)
			}
			continue
		}

		itemRole := "user"
		if role == "assistant" {
			itemRole = "assistant"
		}
		content := messageContent(msg.Content, itemRole)
		if len(content) == 0 {
			continue
		}
		out.Input = append(out.Input, responsesItem{
			Type: "message", Role: itemRole, Content: content,
		})
	}
	out.Instructions = strings.Join(instructions, "\n\n")

	for _, tool := range in.Tools {
		name := strings.TrimSpace(tool.Function.Name)
		if name == "" {
			continue
		}
		out.Tools = append(out.Tools, responsesTool{
			Type:        "function",
			Name:        name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
			// Matching ai.responsesParamsFrom: the archive's schemas are guidance
			// rather than contracts, and strict mode rejects several outright.
			Strict: false,
		})
	}
	if len(out.Tools) > 0 {
		out.ToolChoice = in.ToolChoice
	}

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

// chatContentPart is one part of a multi-part chat message, which is how OCR
// sends the document: an image_url part for a scan or a file part for a PDF,
// both carrying a base64 data URI rather than a URL the backend would fetch.
type chatContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url"`
	File *struct {
		Filename string `json:"filename"`
		FileData string `json:"file_data"`
	} `json:"file"`
}

// messageContent translates a chat message's content, either a plain string or
// an array of typed parts, into Responses parts. A part of a kind not named
// below is dropped rather than guessed at: an unknown shape earns an opaque 400
// from the backend.
func messageContent(raw json.RawMessage, kind string) []responsesContent {
	if len(raw) == 0 {
		return nil
	}
	textType := "input_text"
	if kind == "assistant" {
		textType = "output_text"
	}

	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		if strings.TrimSpace(plain) == "" {
			return nil
		}
		return []responsesContent{{Type: textType, Text: plain}}
	}

	var parts []chatContentPart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return nil
	}
	out := make([]responsesContent, 0, len(parts))
	for _, part := range parts {
		switch {
		case part.Text != "":
			out = append(out, responsesContent{Type: textType, Text: part.Text})
		case part.ImageURL != nil && part.ImageURL.URL != "":
			// input_image carries the URL on the part itself, not nested.
			out = append(out, responsesContent{Type: "input_image", ImageURL: part.ImageURL.URL})
		case part.File != nil && part.File.FileData != "":
			name := part.File.Filename
			if name == "" {
				name = "document"
			}
			out = append(out, responsesContent{
				Type: "input_file", Filename: name, FileData: part.File.FileData,
			})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// messageText flattens a chat message's content to its text, for the system
// role, which the Responses API has only an instructions string for.
func messageText(raw json.RawMessage) string {
	var b strings.Builder
	for _, part := range messageContent(raw, "user") {
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
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls,omitempty"`
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
	Role      string          `json:"role,omitempty"`
	Content   string          `json:"content,omitempty"`
	ToolCalls []chunkToolCall `json:"tool_calls,omitempty"`
}

// chunkToolCall is a tool call inside a stream. Whole calls are emitted here
// rather than fragments, but the index is still required to place them.
type chunkToolCall struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatToolCallFunc `json:"function"`
}

// responsesEvent is the subset of the Codex SSE stream that carries an answer.
type responsesEvent struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
	// Item carries a completed output item on response.output_item.done, where
	// a function call arrives whole, arguments included.
	Item     *responsesOutputItem `json:"item"`
	Response *struct {
		Status string                `json:"status"`
		Output []responsesOutputItem `json:"output"`
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

// responsesOutputItem is one item the model produced. Only function calls are
// read from it: the text arrives as deltas.
type responsesOutputItem struct {
	Type      string `json:"type"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func (i responsesOutputItem) toolCall() (chatToolCall, bool) {
	if i.Type != "function_call" || strings.TrimSpace(i.Name) == "" {
		return chatToolCall{}, false
	}
	args := i.Arguments
	if strings.TrimSpace(args) == "" {
		// A call with no arguments comes back with the field empty rather than
		// as "{}", and every caller here feeds it straight to json.Unmarshal.
		args = "{}"
	}
	return chatToolCall{
		ID:       i.CallID,
		Type:     "function",
		Function: chatToolCallFunc{Name: i.Name, Arguments: args},
	}, true
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
	var toolCalls []chatToolCall
	var completed []responsesOutputItem
	var failure error
	err := scanSSE(resp.Body, func(event responsesEvent) error {
		switch event.Type {
		case "response.output_text.delta":
			text.WriteString(event.Delta)
		case "response.output_item.done":
			if event.Item == nil {
				return nil
			}
			if call, ok := event.Item.toolCall(); ok {
				toolCalls = append(toolCalls, call)
			}
		case "response.completed":
			usage = event.usage()
			if event.Response != nil {
				completed = event.Response.Output
			}
		case "response.failed", "response.incomplete", "error":
			failure = event.failure()
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// The per-item events are the primary source; the completed response is read
	// only when none arrived, so neither reporting style is counted twice.
	if len(toolCalls) == 0 {
		for _, item := range completed {
			if call, ok := item.toolCall(); ok {
				toolCalls = append(toolCalls, call)
			}
		}
	}
	if failure != nil {
		// A partial answer is worth more than none to every caller here, but a
		// stream that produced nothing is a failure, not an empty reply.
		if text.Len() == 0 && len(toolCalls) == 0 {
			return nil, failure
		}
		logger.Warn("the ChatGPT response ended early; keeping the partial answer",
			"model", model, slog.Any("error", failure))
	}

	// A model that asked for a tool has not finished answering, and the search
	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}
	body, err := json.Marshal(chatCompletion{
		ID:      completionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []chatChoice{{
			Index: 0,
			Message: chatOutMessage{
				Role:      "assistant",
				Content:   text.String(),
				ToolCalls: toolCalls,
			},
			FinishReason: finish,
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

// streamingResponse re-emits the Codex stream as chat completion chunks, so
// the SDK's own stream decoder sees the shape it expects.
func streamingResponse(resp *http.Response, model string) *http.Response {
	pr, pw := io.Pipe()

	// Cloned before the goroutine starts, so nothing reads the upstream
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
		// Tool calls are emitted whole, one chunk each. No caller streams a
		// tool-bearing request today, but a middleware that dropped them here
		// would be lossy, and silently.
		toolIndex := 0
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
			case "response.output_item.done":
				if event.Item == nil {
					return nil
				}
				call, ok := event.Item.toolCall()
				if !ok {
					return nil
				}
				index := toolIndex
				toolIndex++
				return writeChunk(chatChunk{
					ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
					Choices: []chunkChoice{{Delta: chunkDelta{ToolCalls: []chunkToolCall{{
						Index:    index,
						ID:       call.ID,
						Type:     call.Type,
						Function: call.Function,
					}}}}},
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
		if toolIndex > 0 {
			stop = "tool_calls"
		}
		final := chatChunk{
			ID: id, Object: "chat.completion.chunk", Created: created, Model: model,
			Choices: []chunkChoice{{Delta: chunkDelta{}, FinishReason: &stop}},
		}
		// Usage rides the final chunk, which is where stream_options puts it and
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
func cloneResponseHead(resp *http.Response) *http.Response {
	out := *resp
	out.Header = resp.Header.Clone()
	if out.Header == nil {
		out.Header = http.Header{}
	}
	return &out
}

// scanSSE reads an event stream. Content-Type is never consulted: the Codex
// backend sends the stream without one, and a reader that branched on it would
// decide the body was JSON and fail on the first `data:`.
func scanSSE(r io.Reader, onEvent func(responsesEvent) error) error {
	scanner := bufio.NewScanner(r)
	// Deltas are small, but a single completed event carries the whole response
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			// event: lines and blank separators. The type is inside the JSON too.
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
