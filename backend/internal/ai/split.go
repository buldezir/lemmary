package ai

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
	"lemmary/backend/internal/logfmt"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/strutil"
)

// splitPromptTotalChars budgets the page text one detection request may carry.
// The per-page share shrinks as the page count grows, so a 100 page scan still
// fits in one request instead of being truncated to the first few pages.
const splitPromptTotalChars = 30000

// splitPromptMinPageChars keeps every page represented even in a long file; a
// document boundary is usually visible in the first lines of a page.
const splitPromptMinPageChars = 200

// PageText is the text found on one page of a document being split.
type PageText struct {
	Page int
	Text string
}

// Splitter proposes where a multi-document PDF should be cut.
type Splitter interface {
	Name() string
	Model() string
	DetectSplitPoints(ctx context.Context, pages []PageText) (*models.SplitSuggestion, error)
}

func NewSplitter(sdk, apiKey, model, baseURL string, timeout time.Duration, logger *slog.Logger) Splitter {
	return NewOpenAIClient(sdk, apiKey, model, baseURL, "", "", timeout, logger)
}

const splitSystemPrompt = `You find document boundaries in a single PDF that holds several separate documents scanned into one file.

You are given the text of each page in order. Decide which consecutive pages belong to the same document.

Return ONLY valid JSON of this shape:
{"parts": [{"from": 1, "to": 2, "title": "short label"}]}

Rules:
- from and to are inclusive 1-based page numbers.
- Parts must be ordered, must not overlap, and together must cover every page exactly once.
- A page that continues the previous document (a second page of the same invoice, a continued table, a signature page) belongs to the same part.
- Start a new part when a page begins a new document: a new letterhead or sender, a new invoice or reference number, a new date and salutation, or an unrelated subject.
- title is a short label naming the document, in the language of the source document.
- If the whole file is one document, return a single part covering every page.

Do not include markdown or explanation.`

func (c *OpenAIClient) DetectSplitPoints(ctx context.Context, pages []PageText) (*models.SplitSuggestion, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("AI API key is not configured")
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("no pages to inspect")
	}

	userMessage := buildSplitUserMessage(pages)
	c.logger.Info("split detection starting",
		"provider", c.Name(),
		"model", c.model,
		"pages", len(pages),
		"sent_chars", len(userMessage),
	)

	requestStart := time.Now()
	chatResp, err := c.complete(ctx, openai.ChatCompletionNewParams{
		Model: shared.ChatModel(c.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage(splitSystemPrompt),
			openai.UserMessage(userMessage),
		},
		ResponseFormat: openai.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONObject: &shared.ResponseFormatJSONObjectParam{},
		},
		Temperature: CompletionTemperature(c.model, 0.1),
	}, "purpose", "split", "messages", 2)
	if err != nil {
		c.logger.Error("request failed",
			logfmt.Duration("duration", time.Since(requestStart)),
			slog.Any("error", err),
		)
		return nil, fmt.Errorf("openai chat completion: %w", err)
	}
	c.logger.Info("response",
		"choices", len(chatResp.Choices),
		logfmt.Duration("duration", time.Since(requestStart)),
	)

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("openai returned no choices")
	}

	content := chatResp.Choices[0].Message.Content
	suggestion, err := models.ParseSplitSuggestion(content)
	if err != nil {
		c.logger.Error("parse failed",
			"content_chars", len(content),
			slog.Any("error", err),
		)
		return nil, err
	}
	c.logger.Info("split detection complete", "parts", len(suggestion.Parts))
	return suggestion, nil
}

// buildSplitUserMessage lays the pages out as labelled blocks so the model can
// answer in page numbers.
func buildSplitUserMessage(pages []PageText) string {
	budget := splitPromptTotalChars / len(pages)
	if budget < splitPromptMinPageChars {
		budget = splitPromptMinPageChars
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("The file has %d pages.\n", len(pages)))
	for _, page := range pages {
		text := strings.TrimSpace(page.Text)
		if text == "" {
			// Said explicitly: a page with no text is a signal in itself (a
			// separator sheet or a photo), not a page to silently omit.
			text = "(no text on this page)"
		}
		fmt.Fprintf(&b, "\n--- PAGE %d ---\n%s\n", page.Page, strutil.Truncate(text, budget))
	}
	return b.String()
}
