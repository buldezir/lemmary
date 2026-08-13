package ocr

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"paperless-go/backend/internal/aiprovider"
)

const mistralOCRMaxFileBytes = 10 * 1024 * 1024

type MistralProvider struct {
	apiKey  string
	model   string
	baseURL string
	client  *http.Client
	logger  *slog.Logger
}

func NewMistralProvider(apiKey, model, baseURL string, timeout time.Duration, logger *slog.Logger) *MistralProvider {
	if timeout <= 0 {
		timeout = 40 * time.Second
	}
	return &MistralProvider{
		apiKey:  apiKey,
		model:   model,
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

func (p *MistralProvider) Name() string {
	return "mistral"
}

func (p *MistralProvider) ExtractText(ctx context.Context, filePath string, mimeType string) (string, error) {
	start := time.Now()

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file for OCR: %w", err)
	}
	if len(data) > mistralOCRMaxFileBytes {
		return "", fmt.Errorf("mistral OCR supports files up to %d bytes (got %d)", mistralOCRMaxFileBytes, len(data))
	}

	effectiveMime := effectiveMimeType(mimeType, filePath)
	docType, dataURL, err := mistralDocumentInput(effectiveMime, data)
	if err != nil {
		return "", err
	}

	p.logger.Info("mistral starting",
		"file", filepath.Base(filePath),
		"mime", effectiveMime,
		"doc_type", docType,
		"bytes", len(data),
	)

	text, err := p.requestOCR(ctx, docType, dataURL)
	if err != nil {
		p.logger.Error("mistral failed",
			"file", filepath.Base(filePath),
			"duration", time.Since(start).Round(time.Millisecond),
			slog.Any("error", err),
		)
		return "", err
	}

	p.logger.Info("mistral complete",
		"file", filepath.Base(filePath),
		"chars", len(text),
		"duration", time.Since(start).Round(time.Millisecond),
	)
	return text, nil
}

func mistralDocumentInput(mimeType string, data []byte) (docType, dataURL string, err error) {
	encoded := base64.StdEncoding.EncodeToString(data)

	switch mimeType {
	case "application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return "document_url", "data:" + mimeType + ";base64," + encoded, nil
	case "image/jpeg", "image/png", "image/webp", "image/avif", "image/tiff", "image/gif":
		return "image_url", "data:" + mimeType + ";base64," + encoded, nil
	default:
		return "", "", fmt.Errorf("mistral OCR does not support mime type %s", mimeType)
	}
}

type mistralOCRRequest struct {
	Model    string          `json:"model"`
	Document mistralDocument `json:"document"`
}

type mistralDocument struct {
	Type        string `json:"type"`
	DocumentURL string `json:"document_url,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
}

type mistralOCRResponse struct {
	Pages []mistralOCRPage `json:"pages"`
}

type mistralOCRPage struct {
	Index    int    `json:"index"`
	Markdown string `json:"markdown"`
}

type mistralAPIError struct {
	Message string `json:"message"`
}

func (p *MistralProvider) requestOCR(ctx context.Context, docType, dataURL string) (string, error) {
	doc := mistralDocument{Type: docType}
	if docType == "document_url" {
		doc.DocumentURL = dataURL
	} else {
		doc.ImageURL = dataURL
	}

	body, err := json.Marshal(mistralOCRRequest{
		Model:    p.model,
		Document: doc,
	})
	if err != nil {
		return "", fmt.Errorf("marshal mistral OCR request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/ocr", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create mistral OCR request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	aiprovider.LogRequest(p.logger, aiprovider.SDKMistral, http.MethodPost, req.URL.String(), p.model, "purpose", "ocr", "doc_type", docType)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mistral OCR request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read mistral OCR response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr mistralAPIError
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.Message != "" {
			return "", fmt.Errorf("mistral OCR: %s", apiErr.Message)
		}
		return "", fmt.Errorf("mistral OCR: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var ocrResp mistralOCRResponse
	if err := json.Unmarshal(respBody, &ocrResp); err != nil {
		return "", fmt.Errorf("decode mistral OCR response: %w", err)
	}

	parts := make([]string, 0, len(ocrResp.Pages))
	for _, page := range ocrResp.Pages {
		if text := strings.TrimSpace(page.Markdown); text != "" {
			parts = append(parts, text)
		}
	}

	text := strings.Join(parts, "\n\n")
	if text == "" {
		return "", fmt.Errorf("mistral OCR returned empty text")
	}

	return text, nil
}
