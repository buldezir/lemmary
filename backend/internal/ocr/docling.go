package ocr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/logfmt"
)

// doclingMaxFileBytes bounds what is sent to the sidecar.
//
// Binary megabytes, unlike the Mistral cap: this limit is ours rather than
// somebody else's documented number, and it exists only so an oversized file
// fails here instead of after minutes of upload into a container that will
// refuse it. 32 MiB comfortably clears the 20 MiB documents.file cap.
const doclingMaxFileBytes = 32 << 20

// DoclingProvider reads documents through a docling-serve container the
// operator runs themselves.
//
// The whole exchange is one multipart POST that returns markdown, which is the
// same shape MistralProvider already produces, so nothing downstream can tell
// the difference between a page read locally and a page read in Paris.
type DoclingProvider struct {
	baseURL string
	engine  string
	apiKey  string
	client  *http.Client
	logger  *slog.Logger
}

// NewDoclingProvider builds a client for docling-serve at baseURL.
//
// engine is the bound OCR model, which for this SDK names docling's OCR engine
// (rapidocr, easyocr, tesserocr, tesseract) rather than a model. Empty leaves
// the choice to the server's own default, which is the right answer for almost
// everyone and the reason the binding is optional.
//
// apiKey is likewise optional. docling-serve enforces a key only when it was
// started with DOCLING_SERVE_API_KEY, which the shipped overlay does not do --
// the sidecar publishes no port and is reachable only from the app container.
// An operator who exposed it further sets both, and OCR_API_KEY carries it.
func NewDoclingProvider(baseURL, engine, apiKey string, timeout time.Duration, logger *slog.Logger) *DoclingProvider {
	if timeout <= 0 {
		timeout = 40 * time.Second
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &DoclingProvider{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		engine:  strings.TrimSpace(engine),
		apiKey:  strings.TrimSpace(apiKey),
		client:  &http.Client{Timeout: timeout},
		logger:  logger,
	}
}

func (p *DoclingProvider) Name() string {
	return aiprovider.SDKDocling
}

// MaxConcurrency is 1: the shipped overlay runs one uvicorn worker, and docling
// already threads inside a single conversion, so a second request in flight
// takes cores away from the first rather than adding any.
func (p *DoclingProvider) MaxConcurrency() int { return 1 }

func (p *DoclingProvider) ExtractText(ctx context.Context, filePath string, mimeType string) (string, error) {
	start := time.Now()

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file for OCR: %w", err)
	}
	if len(data) > doclingMaxFileBytes {
		return "", fmt.Errorf("docling OCR supports files up to %d bytes (got %d)", doclingMaxFileBytes, len(data))
	}

	effectiveMime := effectiveMimeType(mimeType, filePath)
	if !doclingSupports(effectiveMime) {
		return "", fmt.Errorf("docling OCR does not support mime type %s", effectiveMime)
	}

	p.logger.Info("docling starting",
		"file", filepath.Base(filePath),
		"mime", effectiveMime,
		"engine", p.engine,
		"bytes", len(data),
	)

	text, err := p.convert(ctx, filepath.Base(filePath), effectiveMime, data)
	if err != nil {
		p.logger.Error("docling failed",
			"file", filepath.Base(filePath),
			logfmt.Duration("duration", time.Since(start)),
			slog.Any("error", err),
		)
		return "", err
	}

	p.logger.Info("docling complete",
		"file", filepath.Base(filePath),
		"chars", len(text),
		logfmt.Duration("duration", time.Since(start)),
	)
	return text, nil
}

// doclingSupports is everything docling's converter reads that can reach the
// OCR step. Broader than Mistral's list because docling parses office formats
// natively; narrower than docling's full catalogue because the rest cannot get
// here -- textextract claims txt, csv, docx and xlsx before OCR is consulted,
// so those appear only on the /ocr-test page, where answering is still correct.
func doclingSupports(mimeType string) bool {
	switch mimeType {
	case "application/pdf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"text/csv",
		"text/plain",
		"text/html",
		// No image/avif: docling decodes through Pillow, where AVIF needs a
		// plugin that is not in the image. The documents.file allowlist has no
		// avif either, so it can only arrive from the OCR test page -- where a
		// named refusal beats an opaque 500 from the sidecar.
		"image/jpeg", "image/png", "image/webp", "image/tiff", "image/gif", "image/bmp":
		return true
	default:
		return false
	}
}

type doclingResponse struct {
	Document doclingDocument `json:"document"`
	Status   string          `json:"status"`
	Errors   []doclingError  `json:"errors"`
}

type doclingDocument struct {
	Filename  string `json:"filename"`
	MDContent string `json:"md_content"`
}

// doclingError is one entry of the response's errors array. docling-serve has
// shipped both a bare string and an object here depending on the failure, so
// the type absorbs either rather than failing the whole decode over the shape
// of an error message.
type doclingError struct {
	ComponentType string `json:"component_type"`
	ModuleName    string `json:"module_name"`
	ErrorMessage  string `json:"error_message"`

	raw string
}

func (e *doclingError) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &e.raw)
	}
	type alias doclingError
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	*e = doclingError(out)
	return nil
}

func (e doclingError) String() string {
	if e.raw != "" {
		return e.raw
	}
	if e.ErrorMessage == "" {
		return ""
	}
	if e.ModuleName == "" {
		return e.ErrorMessage
	}
	return e.ModuleName + ": " + e.ErrorMessage
}

func (p *DoclingProvider) convert(ctx context.Context, fileName, mimeType string, data []byte) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	fields := [][2]string{
		{"to_formats", "md"},
		{"do_ocr", "true"},
		// The default, embedded, inlines every figure as a base64 data URI --
		// into ocr_text, into the search index, and into every embedding built
		// from the passage. A placeholder keeps the reading order intact and
		// costs a handful of bytes.
		{"image_export_mode", "placeholder"},
	}
	// Everything else -- ocr_engine, ocr_lang, pdf_backend, table_mode -- is
	// left to the server's own defaults on purpose. Each is an enum whose
	// accepted spellings have moved between docling releases, and each one sent
	// is another way for an image bump to become a 422 on every document. The
	// engine is sent only when an admin bound one deliberately.
	if p.engine != "" {
		fields = append(fields, [2]string{"ocr_engine", p.engine})
	}
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			return "", fmt.Errorf("build docling request: %w", err)
		}
	}

	// WriteField's own file part sends application/octet-stream, and docling
	// sniffs the format from the part's content type before it looks at the
	// extension. Building the header by hand is what makes an upload whose
	// name we generated (lemmary-doc-*.pdf) still arrive as a PDF.
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition",
		fmt.Sprintf(`form-data; name="files"; filename=%q`, fileName))
	header.Set("Content-Type", mimeType)
	part, err := writer.CreatePart(header)
	if err != nil {
		return "", fmt.Errorf("build docling request: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return "", fmt.Errorf("build docling request: %w", err)
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("build docling request: %w", err)
	}

	endpoint := p.baseURL + "/v1/convert/file"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body.Bytes()))
	if err != nil {
		return "", fmt.Errorf("create docling request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		req.Header.Set("X-Api-Key", p.apiKey)
	}

	aiprovider.LogRequest(p.logger, aiprovider.SDKDocling, http.MethodPost, endpoint, p.engine, "purpose", "ocr", "mime", mimeType)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("docling OCR request: %w", err)
	}
	defer resp.Body.Close()

	// Bounded for the same reason as the Mistral read: base_url is
	// admin-configurable, so the response size is not fully trusted. Sized
	// against a thousand pages of markdown, which is the page ceiling in
	// internal/limits.
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return "", fmt.Errorf("read docling OCR response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("docling OCR: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out doclingResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decode docling OCR response: %w", err)
	}

	text := strings.TrimSpace(out.Document.MDContent)
	if text == "" {
		// A conversion that failed per-document still answers 200 with an empty
		// md_content and a populated errors array; without this the operator
		// would see only "returned empty text" for a password-protected PDF.
		if detail := doclingErrorDetail(out.Errors); detail != "" {
			return "", fmt.Errorf("docling OCR: %s", detail)
		}
		return "", fmt.Errorf("docling OCR returned empty text")
	}
	return text, nil
}

func doclingErrorDetail(errs []doclingError) string {
	parts := make([]string, 0, len(errs))
	for _, e := range errs {
		if msg := strings.TrimSpace(e.String()); msg != "" {
			parts = append(parts, msg)
		}
	}
	return strings.Join(parts, "; ")
}
