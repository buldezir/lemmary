package ocr

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/logfmt"
)

const llmOCRMaxFileBytes = 10 * 1024 * 1024

const llmOCRPrompt = "Extract all text from this document. Return plain text only, preserving reading order. Do not add commentary."

// LLMProvider reads a document by sending it to a language model.
//
// The client is ai.OpenAIClient rather than an openai.Client of its own: which
// endpoint a model is served on, and which of the two SDKs speaks to it, is
// that type's business, and building a second client here meant two places had
// to agree about it. The four fields this struct used to carry -- sdk, base
// URL, client, and the request options behind them -- all live there.
type LLMProvider struct {
	llm    *ai.OpenAIClient
	model  string
	logger *slog.Logger
}

func NewLLMProvider(p aiprovider.Provider, model string, timeout time.Duration, logger *slog.Logger, extra ...option.RequestOption) *LLMProvider {
	if timeout <= 0 {
		timeout = 40 * time.Second
	}
	return &LLMProvider{
		llm:    ai.NewOpenAIClient(p.SDK, p.APIKey, model, p.BaseURL, "", "", timeout, logger, extra...),
		model:  model,
		logger: logger,
	}
}

func (p *LLMProvider) Name() string { return p.llm.Name() }

func (p *LLMProvider) ExtractText(ctx context.Context, filePath string, mimeType string) (string, error) {
	start := time.Now()
	ctx = aiprovider.EnsureSession(ctx, "ocr")

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file for OCR: %w", err)
	}
	if len(data) > llmOCRMaxFileBytes {
		return "", fmt.Errorf("LLM OCR supports files up to %d bytes (got %d)", llmOCRMaxFileBytes, len(data))
	}

	effectiveMime := effectiveMimeType(mimeType, filePath)
	parts, err := LLMUserContentParts(filepath.Base(filePath), effectiveMime, data)
	if err != nil {
		return "", err
	}

	p.logger.Info("llm ocr starting",
		"sdk", p.llm.Name(),
		"model", p.model,
		"file", filepath.Base(filePath),
		"mime", effectiveMime,
		"bytes", len(data),
	)

	ocrParams := openai.ChatCompletionNewParams{
		Model: shared.ChatModel(p.model),
		Messages: []openai.ChatCompletionMessageParamUnion{
			openai.SystemMessage("You transcribe documents for an archive. Return only the extracted text."),
			openai.UserMessage(parts),
		},
		Temperature: ai.CompletionTemperature(p.model, 0),
	}
	chatResp, err := p.llm.Complete(ctx, ocrParams, "purpose", "ocr", "messages", 2)
	if err != nil {
		p.logger.Error("llm ocr failed",
			"file", filepath.Base(filePath),
			logfmt.Duration("duration", time.Since(start)),
			slog.Any("error", err),
		)
		return "", fmt.Errorf("llm ocr: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", fmt.Errorf("llm ocr returned no choices")
	}

	text := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("LLM OCR returned empty text")
	}
	p.logger.Info("llm ocr complete",
		"file", filepath.Base(filePath),
		"chars", len(text),
		logfmt.Duration("duration", time.Since(start)),
	)
	return text, nil
}

// LLMUserContentParts builds the multimodal user content for LLM OCR.
func LLMUserContentParts(filename, mimeType string, data []byte) ([]openai.ChatCompletionContentPartUnionParam, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty file")
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	dataURL := "data:" + mimeType + ";base64," + encoded

	parts := []openai.ChatCompletionContentPartUnionParam{
		openai.TextContentPart(llmOCRPrompt),
	}
	if strings.HasPrefix(mimeType, "image/") {
		parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
			URL: dataURL,
		}))
		return parts, nil
	}

	// Chat-completions file parts only accept PDFs; anything else (docx, pptx)
	// would come back as an opaque upstream 400 instead of a clear error.
	if mimeType != "application/pdf" {
		return nil, fmt.Errorf("LLM OCR does not support mime type %s; use a PDF or image, or a different OCR provider", mimeType)
	}

	if filename == "" {
		filename = "document"
	}
	parts = append(parts, openai.FileContentPart(openai.ChatCompletionContentPartFileFileParam{
		Filename: openai.String(filename),
		FileData: openai.String(dataURL),
	}))
	return parts, nil
}
