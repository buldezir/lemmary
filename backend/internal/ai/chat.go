package ai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/logfmt"
	"lemmary/backend/internal/strutil"
	"lemmary/backend/internal/websearch"
)

// maxChatToolRounds bounds how many times document chat may reach the web
// before it has to answer. Unlike research, this endpoint streams nothing and
// the user is staring at a spinner, so the loop is short by design.
const maxChatToolRounds = 4

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Chatter interface {
	// Chat answers about one document. web backs web_search and web_fetch; nil
	// leaves both undeclared, and the call is then the single completion it has
	// always been.
	Chat(ctx context.Context, ocrText string, messages []ChatMessage, web *websearch.Tavily) (string, error)
}

func buildChatSystemPrompt(ocrText string, web bool) string {
	prompt := `You are a helpful assistant answering questions about a document.
Use the OCR text below as your primary source. If the answer is not in the document, say so clearly.
Be concise and accurate.`
	if web {
		prompt += `
You can also reach the public web with web_search and web_fetch, for what the document cannot say: current prices, rates and rules, a company's present details, anything that changed after it was written.
The document stays the primary source. A search snippet is a reason to fetch the page, not the whole of what it says.
Cite a claim taken from the web as [Page title](https://...), with the URL the tool returned, and say which claims came from the web rather than from the document.
Web calls are limited and billed; make them count.`
	}
	return prompt + fmt.Sprintf(`

Document OCR text:

%s`, strutil.Truncate(ocrText, 12000))
}

func (c *OpenAIClient) Chat(ctx context.Context, ocrText string, messages []ChatMessage, web *websearch.Tavily) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("AI API key is not configured")
	}
	// Fills in only for a caller with no session of its own to name.
	ctx = aiprovider.EnsureSession(ctx, "chat")

	apiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	apiMessages = append(apiMessages, openai.SystemMessage(buildChatSystemPrompt(ocrText, web != nil)))
	for _, msg := range messages {
		role := strings.TrimSpace(msg.Role)
		content := strings.TrimSpace(msg.Content)
		if role == "" || content == "" {
			continue
		}
		if role != "user" && role != "assistant" {
			return "", fmt.Errorf("invalid message role: %s", role)
		}
		if role == "user" {
			apiMessages = append(apiMessages, openai.UserMessage(content))
		} else {
			apiMessages = append(apiMessages, openai.AssistantMessage(content))
		}
	}

	if len(apiMessages) < 2 {
		return "", fmt.Errorf("at least one user message is required")
	}

	if web == nil {
		chatResp, err := c.completeChatTurn(ctx, apiMessages, nil, false, 0)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
	}
	return c.chatWithWeb(ctx, apiMessages, web)
}

// chatWithWeb is the Search agent's fixed-round loop (see search.go): tools stay
// declared on every round because OpenAI-compatible endpoints reject a bare
// tool_choice with no tools array, and the last round flips the choice to none
// so there is always an answer rather than a fifth tool call.
func (c *OpenAIClient) chatWithWeb(
	ctx context.Context,
	apiMessages []openai.ChatCompletionMessageParamUnion,
	web *websearch.Tavily,
) (string, error) {
	tools := []openai.ChatCompletionToolParam{webSearchTool(), webFetchTool()}
	budget := &webBudget{}

	for round := 0; round <= maxChatToolRounds; round++ {
		allowTools := round < maxChatToolRounds
		chatResp, err := c.completeChatTurn(ctx, apiMessages, tools, allowTools, round)
		if err != nil {
			return "", err
		}

		msg := chatResp.Choices[0].Message
		nativeCalls := msg.ToolCalls
		var dsmlCalls []parsedToolCall
		if len(nativeCalls) == 0 {
			dsmlCalls = parseDSMLToolCalls(msg.Content)
		}
		if len(nativeCalls) == 0 && len(dsmlCalls) == 0 {
			return stripDSMLMarkup(strings.TrimSpace(msg.Content)), nil
		}

		if len(nativeCalls) > 0 {
			apiMessages = append(apiMessages, msg.ToParam())
			for _, call := range nativeCalls {
				result, _ := runWebTool(ctx, web, budget, call.ID, call.Function.Name, call.Function.Arguments, nil)
				apiMessages = append(apiMessages, openai.ToolMessage(result.Content, call.ID))
			}
			continue
		}
		// DSML models put tool calls in content; feed results back as a user message.
		apiMessages = append(apiMessages, openai.AssistantMessage(msg.Content))
		results := make([]toolExecResult, 0, len(dsmlCalls))
		for _, call := range dsmlCalls {
			result, _ := runWebTool(ctx, web, budget, call.ID, call.Name, call.Arguments, nil)
			results = append(results, result)
		}
		apiMessages = append(apiMessages, openai.UserMessage(formatDSMLToolResults(results)))
	}

	// Unreachable: the final round declares tool_choice none, so it answers.
	return "", fmt.Errorf("chat did not produce an answer")
}

func (c *OpenAIClient) completeChatTurn(
	ctx context.Context,
	apiMessages []openai.ChatCompletionMessageParamUnion,
	tools []openai.ChatCompletionToolParam,
	allowTools bool,
	round int,
) (*openai.ChatCompletion, error) {
	params := openai.ChatCompletionNewParams{
		Model:       shared.ChatModel(c.model),
		Messages:    apiMessages,
		Temperature: CompletionTemperature(c.model, 0.3),
	}
	if len(tools) > 0 {
		params.Tools = tools
		choice := "none"
		if allowTools {
			choice = "auto"
		}
		params.ToolChoice = openai.ChatCompletionToolChoiceOptionUnionParam{OfAuto: openai.String(choice)}
	}

	requestStart := time.Now()
	chatResp, err := c.Complete(ctx, params,
		"purpose", "chat",
		"round", round,
		"allow_tools", allowTools,
		"messages", len(apiMessages),
	)
	if err != nil {
		c.logger.Error("chat request failed",
			logfmt.Duration("duration", time.Since(requestStart)),
			slog.Any("error", err),
		)
		return nil, fmt.Errorf("openai chat completion: %w", err)
	}
	c.logger.Info("chat response",
		"choices", len(chatResp.Choices),
		logfmt.Duration("duration", time.Since(requestStart)),
	)
	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}
	return chatResp, nil
}

func NewChatter(sdk, apiKey, model, baseURL string, timeout time.Duration, logger *slog.Logger, extra ...option.RequestOption) Chatter {
	return NewOpenAIClient(sdk, apiKey, model, baseURL, "", "", timeout, logger, extra...)
}
