package ocr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"paperless-go/backend/internal/aiprovider"
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
