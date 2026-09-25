package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"

	"lemmary/backend/internal/aiprovider"
)

// ErrTranslationTruncated is a reply the model stopped early, usually at its
// output limit: storing it would serve half a document as the translation.
var ErrTranslationTruncated = errors.New("translation was cut off before the end of the document")

func buildTranslateSystemPrompt(language string) string {
	return fmt.Sprintf("You translate OCR text of a scanned document into the language with ISO 639-1 code %q. "+
		"Preserve line breaks, lists, numbers, dates and amounts. Leave names and text already in that language unchanged. "+
		"Reply with the translation only: no preamble, no notes, no code fences.", language)
}

// Translate returns ocrText in the client's result language, sent whole: the
// provider's context window is the only limit.
func (c *OpenAIClient) Translate(ctx context.Context, ocrText string) (string, error) {
	if c.apiKey == "" {
		return "", fmt.Errorf("AI API key is not configured")
	}
	if c.resultLanguage == "" {
		return "", fmt.Errorf("result language is not configured")
	}
	ctx = aiprovider.EnsureSession(ctx, "translate")

	resp, err := c.Complete(ctx, openai.ChatCompletionNewParams{
		Model: shared.ChatModel(c.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(buildTranslateSystemPrompt(c.resultLanguage)),
			openai.UserMessage(ocrText),
		},
		Temperature: CompletionTemperature(c.model, 0.1),
	}, "purpose", "translate", "messages", 2)
	if err != nil {
		return "", fmt.Errorf("openai chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai returned no choices")
	}
	choice := resp.Choices[0]
	if choice.FinishReason == "length" || choice.FinishReason == "content_filter" {
		return "", fmt.Errorf("%w (finish_reason %s)", ErrTranslationTruncated, choice.FinishReason)
	}
	text := strings.TrimSpace(choice.Message.Content)
	if text == "" {
		return "", fmt.Errorf("model returned an empty translation")
	}
	return text, nil
}
