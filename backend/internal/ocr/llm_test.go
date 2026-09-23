package ocr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/pdftool/testpdf"
)

func TestLLMUserContentPartsImage(t *testing.T) {
	t.Parallel()
	parts, err := LLMUserContentParts("scan.png", "image/png", []byte("png-bytes"))
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != 2 {
		t.Fatalf("len=%d", len(parts))
	}
	raw, err := json.Marshal(openai.UserMessage(parts))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"type":"image_url"`) {
		t.Fatalf("expected image_url part: %s", body)
	}
	if !strings.Contains(body, "data:image/png;base64,") {
		t.Fatalf("expected image data URL: %s", body)
	}
	if strings.Contains(body, `"type":"file"`) {
		t.Fatalf("image should not use file part: %s", body)
	}
}

func TestLLMUserContentPartsPDF(t *testing.T) {
	t.Parallel()
	parts, err := LLMUserContentParts("invoice.pdf", "application/pdf", []byte("%PDF"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(openai.UserMessage(parts))
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.Contains(body, `"type":"file"`) {
		t.Fatalf("expected file part: %s", body)
	}
	if !strings.Contains(body, `"filename":"invoice.pdf"`) {
		t.Fatalf("expected filename: %s", body)
	}
	if !strings.Contains(body, "data:application/pdf;base64,") {
		t.Fatalf("expected file data URL: %s", body)
	}
}

func TestLLMUserContentPartsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := LLMUserContentParts("x", "image/png", nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestExtractTextRetriesWithoutTemperatureForUnknownModel(t *testing.T) {
	t.Parallel()
	const model = "azure-custom-deployment"
	if !aiprovider.AllowsCustomTemperature(model) {
		t.Fatalf("%q should send a custom temperature so the retry path is exercised", model)
	}

	var calls atomic.Int64
	var firstBody, secondBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		if n == 1 {
			firstBody = string(raw)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'temperature' does not support 0 with this model. Only the default (1) value is supported.","type":"invalid_request_error","param":"temperature","code":"unsupported_value"}}`))
			return
		}
		secondBody = string(raw)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-ocr",
			"object":  "chat.completion",
			"created": 1,
			"model":   model,
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Invoice INV-1001",
				},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := filepath.Join(dir, "scan.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewLLMProvider(aiprovider.Provider{
		SDK:     aiprovider.SDKOpenAI,
		APIKey:  "test-key",
		BaseURL: srv.URL,
	}, model, 5*time.Second, slog.Default())
	text, err := p.ExtractText(context.Background(), path, "image/png")
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if text != "Invoice INV-1001" {
		t.Fatalf("text = %q", text)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if !strings.Contains(firstBody, `"temperature"`) {
		t.Fatalf("first OCR request should include temperature, got %s", firstBody)
	}
	if strings.Contains(secondBody, `"temperature"`) {
		t.Fatalf("retry should omit temperature, got %s", secondBody)
	}
}

// The production-client case: NewLLMProvider itself must install
// SessionMiddleware.
func TestExtractTextSendsSessionHeaderToOpenCode(t *testing.T) {
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(aiprovider.SessionHeader)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-ocr",
			"object":  "chat.completion",
			"created": 1,
			"model":   "test-model",
			"choices": []map[string]any{{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "Invoice INV-1001",
				},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	path := filepath.Join(dir, "scan.png")
	if err := os.WriteFile(path, []byte("png-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := NewLLMProvider(aiprovider.Provider{
		SDK:     aiprovider.SDKOpenCode,
		APIKey:  "test-key",
		BaseURL: srv.URL + "/zen/go/v1",
	}, "test-model", 5*time.Second, slog.Default())
	ctx := aiprovider.WithSession(context.Background(), "conv123")
	text, err := p.ExtractText(ctx, path, "image/png")
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if text != "Invoice INV-1001" {
		t.Fatalf("text = %q", text)
	}
	if seen != "conv123" {
		t.Errorf("%s = %q, want %q", aiprovider.SessionHeader, seen, "conv123")
	}

	// The other half of the gate: an openai row installs no middleware, so a
	// request to the same address carries nothing.
	seen = "unset"
	plain := NewLLMProvider(aiprovider.Provider{
		SDK:     aiprovider.SDKOpenAI,
		APIKey:  "test-key",
		BaseURL: srv.URL + "/v1",
	}, "test-model", 5*time.Second, slog.Default())
	if _, err := plain.ExtractText(ctx, path, "image/png"); err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if seen != "" {
		t.Errorf("%s = %q, want empty for an openai provider", aiprovider.SessionHeader, seen)
	}
}

// OpenCode Go forwards a chat file part to models that cannot take one, and the
// upstream refusal comes back as a 400; the pages rendered as images can be read.
func TestExtractTextRendersPagesWhenTheFilePartIsRefused(t *testing.T) {
	for _, binary := range []string{"pdfinfo", "pdftoppm"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s not installed", binary)
		}
	}
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(raw))
		w.Header().Set("Content-Type", "application/json")
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"param":"","type":"invalid_request_error","code":"invalid_request_error","message":"Upstream request failed: [invalid_request_error] .messages[1]: file must have a file_id or file_data"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-ocr", "object": "chat.completion", "created": 1, "model": "kimi-k2",
			"choices": []map[string]any{{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": "Page one. Page two."},
				"finish_reason": "stop",
			}},
		})
	}))
	t.Cleanup(srv.Close)

	path := filepath.Join(t.TempDir(), "invoice.pdf")
	if err := os.WriteFile(path, testpdf.Multipage(2), 0o600); err != nil {
		t.Fatal(err)
	}
	p := NewLLMProvider(aiprovider.Provider{
		SDK:     aiprovider.SDKOpenCode,
		APIKey:  "test-key",
		BaseURL: srv.URL + "/zen/go/v1",
	}, "kimi-k2", 5*time.Second, slog.Default())
	text, err := p.ExtractText(context.Background(), path, "application/pdf")
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if text != "Page one. Page two." {
		t.Fatalf("text = %q", text)
	}
	if len(bodies) != 2 {
		t.Fatalf("requests = %d, want the file part then the rendered pages", len(bodies))
	}
	if strings.Contains(bodies[1], `"file_data"`) || strings.Count(bodies[1], "data:image/png;base64,") != 2 {
		t.Fatalf("retry should carry two page images and no file part, got %.300s", bodies[1])
	}
}
