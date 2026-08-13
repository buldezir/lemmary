package ai

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
	"paperless-go/backend/internal/models"
)

type Extractor interface {
	Name() string
	Model() string
	ExtractMetadata(ctx context.Context, ocrText string) (*models.ExtractedMetadata, error)
}

func buildExtractionSystemPrompt(resultLanguage string) string {
	prompt := `You extract structured metadata from OCR document text.
Return ONLY valid JSON with these fields:
- title (string, required)
- purpose (string)
- document_date (string, YYYY-MM-DD or empty)
- document_type (string)
- correspondent (string, primary sender or issuer)
- tags (array of strings)
- people_or_organizations (array of strings)
- summary (string, 1-3 sentences)
- confidence (number between 0 and 1)

Always write title, purpose, summary, tags, and people_or_organizations in the same language as the source document.`

	if resultLanguage != "" {
		prompt += fmt.Sprintf(`

Also include these fields translated into %s:
- title_translated (string)
- purpose_translated (string)
- summary_translated (string)
- document_type_translated (string)
- correspondent_translated (string)
- tags_translated (array of strings) — one translation per tag, same order as tags`, resultLanguage)
	}

	prompt += `

Do not include markdown or explanation.`
	return prompt
}

func (c *OpenAIClient) ExtractMetadata(ctx context.Context, ocrText string) (*models.ExtractedMetadata, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("AI API key is not configured")
	}

	inputChars := len(ocrText)
	sentChars := len(truncate(ocrText, 12000))
	c.logger.Info("extraction starting",
		"provider", c.Name(),
		"model", c.model,
		"prompt_ver", c.promptVer,
		"ocr_chars", inputChars,
		"sent_chars", sentChars,
		"result_lang", c.resultLanguage,
	)

	requestStart := time.Now()
	chatResp, err := c.complete(ctx, openai.ChatCompletionNewParams{
		Model: shared.ChatModel(c.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(buildExtractionSystemPrompt(c.resultLanguage)),
			openai.UserMessage(fmt.Sprintf("Extract metadata from this OCR text:\n\n%s", truncate(ocrText, 12000))),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
		Temperature: openai.Float(0.1),
	}, "purpose", "extract", "messages", 2)
	if err != nil {
		c.logger.Error("request failed",
			"duration", time.Since(requestStart).Round(time.Millisecond),
			slog.Any("error", err),
		)
		return nil, fmt.Errorf("openai chat completion: %w", err)
	}
	c.logger.Info("response",
		"choices", len(chatResp.Choices),
		"duration", time.Since(requestStart).Round(time.Millisecond),
	)

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}

	content := chatResp.Choices[0].Message.Content
	metadata, err := models.ParseExtractedMetadata(content)
	if err != nil {
		c.logger.Error("parse failed",
			"content_chars", len(content),
			slog.Any("error", err),
		)
		return nil, err
	}
	c.logger.Info("extraction complete",
		"confidence", metadata.Confidence,
		"title", truncateForLog(metadata.Title, 80),
		"type", truncateForLog(metadata.DocumentType, 40),
		"tags", len(metadata.Tags),
		"content_chars", len(content),
	)
	return metadata, nil
}

func NewExtractor(sdk, apiKey, model, baseURL, promptVer, resultLanguage string, timeout time.Duration, logger *slog.Logger) Extractor {
	return NewOpenAIClient(sdk, apiKey, model, baseURL, promptVer, resultLanguage, timeout, logger)
}
