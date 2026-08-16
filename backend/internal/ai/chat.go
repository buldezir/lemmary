package ai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Chatter interface {
	Chat(ctx context.Context, ocrText string, messages []ChatMessage) (string, error)
}

func buildChatSystemPrompt(ocrText string) string {
	return fmt.Sprintf(`You are a helpful assistant answering questions about a document.
Use the OCR text below as your primary source. If the answer is not in the document, say so clearly.
Be concise and accurate.

Document OCR text:

%s`, truncate(ocrText, 12000))
}

func (c *OpenAIClient) Chat(ctx context.Context, ocrText string, messages []ChatMessage) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("AI API key is not configured")
	}

	apiMessages := make([]openai.ChatCompletionMessageParamUnion, 0, len(messages)+1)
	apiMessages = append(apiMessages, openai.SystemMessage(buildChatSystemPrompt(ocrText)))
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

	requestStart := time.Now()
	chatResp, err := c.complete(ctx, openai.ChatCompletionNewParams{
		Model:       shared.ChatModel(c.model),
		Messages:    apiMessages,
		Temperature: CompletionTemperature(c.model, 0.3),
	}, "purpose", "chat", "messages", len(apiMessages))
	if err != nil {
		c.logger.Error("chat request failed",
			"duration", time.Since(requestStart).Round(time.Millisecond),
			slog.Any("error", err),
		)
		return "", fmt.Errorf("openai chat completion: %w", err)
	}
	c.logger.Info("chat response",
		"choices", len(chatResp.Choices),
		"duration", time.Since(requestStart).Round(time.Millisecond),
	)

	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}

	return strings.TrimSpace(chatResp.Choices[0].Message.Content), nil
}

func NewChatter(sdk, apiKey, model, baseURL string, timeout time.Duration, logger *slog.Logger) Chatter {
	return NewOpenAIClient(sdk, apiKey, model, baseURL, "", "", timeout, logger)
}
