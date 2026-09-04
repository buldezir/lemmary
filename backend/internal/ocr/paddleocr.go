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

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/logfmt"
)

// paddleOCRMaxFileBytes bounds what is sent to the sidecar. Lower than
// Docling's because PaddleX takes its input base64-encoded inside a JSON body,
// which inflates it by a third and holds the whole thing in the server's memory
// at once.
const paddleOCRMaxFileBytes = 24 << 20

// PaddleX file type discriminators. The serving API does not sniff: it is told.
const (
	paddleFileTypePDF   = 0
	paddleFileTypeImage = 1
)

// Pipelines this provider knows how to drive, named by the bound OCR model.
//
// PaddleX serves one pipeline per process and names the endpoint after it, so
// the "model" binding here selects an endpoint rather than a model. The two
// worth offering are the layout parser, which returns markdown with tables and
// reading order, and the plain recogniser, which returns loose line fragments
// and is faster.
const (
	paddlePipelineStructure = "pp-structurev3"
	paddlePipelineOCR       = "ocr"
)

type PaddleOCRProvider struct {
	baseURL  string
	pipeline string
	client   *http.Client
	logger   *slog.Logger
}

// NewPaddleOCRProvider builds a client for a PaddleX basic-serving container.
//
// pipeline is the bound OCR model: "pp-structurev3" (the default) or "ocr".
// Anything else is passed through as an endpoint path, so an operator serving
// a pipeline we have not named can still reach it without a code change.
func NewPaddleOCRProvider(baseURL, pipeline string, timeout time.Duration, logger *slog.Logger) *PaddleOCRProvider {
	if timeout <= 0 {
		timeout = 40 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	pipeline = strings.ToLower(strings.TrimSpace(pipeline))
	if pipeline == "" {
		pipeline = paddlePipelineStructure
	}
	return &PaddleOCRProvider{
		baseURL:  strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		pipeline: pipeline,
		client:   &http.Client{Timeout: timeout},
		logger:   logger,
	}
}

func (p *PaddleOCRProvider) Name() string {
	return aiprovider.SDKPaddleOCR
}

// MaxConcurrency is 1. PaddleX basic serving is a single-process uvicorn, and
// PP-StructureV3 runs a stack of models per page; concurrent requests queue
// behind each other inside the container whatever we do here, and queueing
// there burns the caller's timeout instead of ours.
func (p *PaddleOCRProvider) MaxConcurrency() int { return 1 }

func (p *PaddleOCRProvider) endpoint() string {
	switch p.pipeline {
	case paddlePipelineStructure:
		return p.baseURL + "/layout-parsing"
	case paddlePipelineOCR:
		return p.baseURL + "/ocr"
	default:
		return p.baseURL + "/" + strings.TrimLeft(p.pipeline, "/")
	}
}

func (p *PaddleOCRProvider) ExtractText(ctx context.Context, filePath string, mimeType string) (string, error) {
	start := time.Now()

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file for OCR: %w", err)
	}
	if len(data) > paddleOCRMaxFileBytes {
		return "", fmt.Errorf("paddleocr OCR supports files up to %d bytes (got %d)", paddleOCRMaxFileBytes, len(data))
	}

	effectiveMime := effectiveMimeType(mimeType, filePath)
	fileType, err := paddleFileType(effectiveMime)
	if err != nil {
		return "", err
	}

	p.logger.Info("paddleocr starting",
		"file", filepath.Base(filePath),
		"mime", effectiveMime,
		"pipeline", p.pipeline,
		"bytes", len(data),
	)

	text, err := p.requestOCR(ctx, fileType, base64.StdEncoding.EncodeToString(data))
	if err != nil {
		p.logger.Error("paddleocr failed",
			"file", filepath.Base(filePath),
			logfmt.Duration("duration", time.Since(start)),
			slog.Any("error", err),
		)
		return "", err
	}

	p.logger.Info("paddleocr complete",
		"file", filepath.Base(filePath),
		"chars", len(text),
		logfmt.Duration("duration", time.Since(start)),
	)
	return text, nil
}

// paddleFileType maps a mime type onto PaddleX's fileType discriminator, and
// refuses everything else. The office formats are a real gap rather than an
// oversight -- PaddleX reads pixels, not OOXML -- so the error names the SDK
// that does handle them instead of leaving the operator to guess.
func paddleFileType(mimeType string) (int, error) {
	switch mimeType {
	case "application/pdf":
		return paddleFileTypePDF, nil
	case "image/jpeg", "image/png", "image/webp", "image/tiff", "image/gif", "image/bmp":
		return paddleFileTypeImage, nil
	default:
		return 0, fmt.Errorf(
			"paddleocr OCR does not support mime type %s (PDFs and images only; bind the %s provider for office documents)",
			mimeType, aiprovider.SDKDocling)
	}
}

type paddleRequest struct {
	File     string `json:"file"`
	FileType int    `json:"fileType"`
}

// paddleResponse carries every shape PaddleX's serving API has answered with.
//
// The response contract has drifted across releases: 3.x documents
// layoutParsingResults[].markdown.text, earlier builds put the text on
// layoutElements[], and the plain /ocr pipeline returns none of that -- only
// prunedResult.rec_texts, the recognised lines. Decoding all three and
// preferring them in that order is what keeps an image bump from silently
// producing empty documents; a struct that knew only the current shape would
// decode cleanly and yield "".
type paddleResponse struct {
	ErrorCode int          `json:"errorCode"`
	ErrorMsg  string       `json:"errorMsg"`
	Result    paddleResult `json:"result"`
}

type paddleResult struct {
	LayoutParsingResults []paddleLayoutResult `json:"layoutParsingResults"`
	OCRResults           []paddleOCRResult    `json:"ocrResults"`
}

type paddleLayoutResult struct {
	// Raw, because this field has been both {"text": "..."} and a bare string.
	// Typing it either way makes the other a decode error for the whole
	// response rather than one missing field.
	Markdown       json.RawMessage    `json:"markdown"`
	LayoutElements []paddleLayoutElem `json:"layoutElements"`
	PrunedResult   paddlePrunedResult `json:"prunedResult"`
}

type paddleMarkdown struct {
	Text string `json:"text"`
}

// markdownText reads either spelling of the markdown field.
func markdownText(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return ""
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err == nil {
			return s
		}
		return ""
	}
	var md paddleMarkdown
	if err := json.Unmarshal(trimmed, &md); err == nil {
		return md.Text
	}
	return ""
}

type paddleLayoutElem struct {
	Label string `json:"label"`
	Text  string `json:"text"`
}

type paddleOCRResult struct {
	PrunedResult paddlePrunedResult `json:"prunedResult"`
}

type paddlePrunedResult struct {
	RecTexts []string `json:"rec_texts"`
}

// text walks the three shapes in order of fidelity. Markdown preserves reading
// order and tables; layout elements preserve reading order only; rec_texts is
// a bag of lines, which is still better than failing.
func (r paddleResponse) text() string {
	pages := make([]string, 0, len(r.Result.LayoutParsingResults)+len(r.Result.OCRResults))

	for _, page := range r.Result.LayoutParsingResults {
		if md := strings.TrimSpace(markdownText(page.Markdown)); md != "" {
			pages = append(pages, md)
			continue
		}
		if joined := joinNonEmpty(elementTexts(page.LayoutElements), "\n\n"); joined != "" {
			pages = append(pages, joined)
			continue
		}
		if joined := joinNonEmpty(page.PrunedResult.RecTexts, "\n"); joined != "" {
			pages = append(pages, joined)
		}
	}
	for _, page := range r.Result.OCRResults {
		if joined := joinNonEmpty(page.PrunedResult.RecTexts, "\n"); joined != "" {
			pages = append(pages, joined)
		}
	}
	return strings.Join(pages, "\n\n")
}

func elementTexts(elems []paddleLayoutElem) []string {
	out := make([]string, 0, len(elems))
	for _, elem := range elems {
		out = append(out, elem.Text)
	}
	return out
}

func joinNonEmpty(parts []string, sep string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, sep)
}

func (p *PaddleOCRProvider) requestOCR(ctx context.Context, fileType int, encoded string) (string, error) {
	body, err := json.Marshal(paddleRequest{File: encoded, FileType: fileType})
	if err != nil {
		return "", fmt.Errorf("marshal paddleocr request: %w", err)
	}

	endpoint := p.endpoint()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create paddleocr request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	aiprovider.LogRequest(p.logger, aiprovider.SDKPaddleOCR, http.MethodPost, endpoint, p.pipeline, "purpose", "ocr", "file_type", fileType)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("paddleocr OCR request: %w", err)
	}
	defer resp.Body.Close()

	// Bounded like the other providers: base_url is admin-configurable, so the
	// response size is not fully trusted. PaddleX answers are wordier than
	// markdown -- every line carries a polygon -- but a thousand pages of them
	// still fits well inside this.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return "", fmt.Errorf("read paddleocr OCR response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr paddleResponse
		if json.Unmarshal(respBody, &apiErr) == nil && apiErr.ErrorMsg != "" {
			return "", fmt.Errorf("paddleocr OCR: %s", apiErr.ErrorMsg)
		}
		return "", fmt.Errorf("paddleocr OCR: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out paddleResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decode paddleocr OCR response: %w", err)
	}
	// A 200 with a non-zero errorCode is PaddleX reporting a per-request
	// failure in the body rather than the status line.
	if out.ErrorCode != 0 {
		msg := strings.TrimSpace(out.ErrorMsg)
		if msg == "" {
			msg = fmt.Sprintf("errorCode %d", out.ErrorCode)
		}
		return "", fmt.Errorf("paddleocr OCR: %s", msg)
	}

	text := strings.TrimSpace(out.text())
	if text == "" {
		return "", fmt.Errorf("paddleocr OCR returned empty text")
	}
	return text, nil
}
