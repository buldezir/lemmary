package ai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go"
)

const extractTestJSON = `{
  "title": "Invoice",
  "purpose": "Pay",
  "document_date": "2024-07-15",
  "document_type": "Invoice",
  "correspondent": "Acme",
  "tags": ["invoice"],
  "people_or_organizations": ["Acme"],
  "summary": "Invoice from Acme.",
  "confidence": 0.9
}`

func TestCompletionTemperature(t *testing.T) {
	t.Parallel()
	if CompletionTemperature("gpt-5-mini", 0.1).Valid() {
		t.Fatal("gpt-5-mini should omit temperature")
	}
	got := CompletionTemperature("gpt-4o-mini", 0.1)
	if !got.Valid() || got.Value != 0.1 {
		t.Fatalf("gpt-4o-mini temperature = %+v, want 0.1", got)
	}
}

func TestIsUnsupportedTemperatureError(t *testing.T) {
	t.Parallel()
	if isUnsupportedTemperatureError(nil) {
		t.Fatal("nil should not match")
	}
	if isUnsupportedTemperatureError(errors.New("rate limited")) {
		t.Fatal("unrelated error should not match")
	}
	apiErr := &openai.Error{
		Param:   "temperature",
		Message: "Unsupported value: 'temperature' does not support 0.1 with this model. Only the default (1) value is supported.",
		Type:    "invalid_request_error",
		Code:    "unsupported_value",
	}
	if !isUnsupportedTemperatureError(apiErr) {
		t.Fatal("openai temperature error should match")
	}
}

func TestExtractMetadataOmitsTemperatureForGPT5(t *testing.T) {
	t.Parallel()
	body := captureChatBody(t, "gpt-5-mini")
	if strings.Contains(body, `"temperature"`) {
		t.Fatalf("gpt-5-mini request should omit temperature, got %s", body)
	}
}

func TestExtractMetadataSendsTemperatureForGPT4o(t *testing.T) {
	t.Parallel()
	body := captureChatBody(t, "gpt-4o-mini")
	if !strings.Contains(body, `"temperature"`) {
		t.Fatalf("gpt-4o-mini request should include temperature, got %s", body)
	}
}

func TestExtractMetadataRetriesWithoutTemperature(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	var firstBody, secondBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		if n == 1 {
			firstBody = string(raw)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Unsupported value: 'temperature' does not support 0.1 with this model. Only the default (1) value is supported.","type":"invalid_request_error","param":"temperature","code":"unsupported_value"}}`))
			return
		}
		secondBody = string(raw)
		writeChatJSON(w, extractTestJSON)
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient("openai", "test-key", "gpt-4o-mini", srv.URL, "v1", "", 5*time.Second, slog.Default())
	meta, err := client.ExtractMetadata(context.Background(), "Invoice from Acme")
	if err != nil {
		t.Fatalf("ExtractMetadata: %v", err)
	}
	if meta.Title != "Invoice" {
		t.Fatalf("title = %q", meta.Title)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if !strings.Contains(firstBody, `"temperature"`) {
		t.Fatalf("first request should include temperature, got %s", firstBody)
	}
	if strings.Contains(secondBody, `"temperature"`) {
		t.Fatalf("retry should omit temperature, got %s", secondBody)
	}
}

func captureChatBody(t *testing.T, model string) string {
	t.Helper()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body = string(raw)
		writeChatJSON(w, extractTestJSON)
	}))
	t.Cleanup(srv.Close)

	client := NewOpenAIClient("openai", "test-key", model, srv.URL, "v1", "", 5*time.Second, slog.Default())
	if _, err := client.ExtractMetadata(context.Background(), "Invoice from Acme"); err != nil {
		t.Fatalf("ExtractMetadata: %v", err)
	}
	return body
}

func writeChatJSON(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1,
		"model":   "test",
		"choices": []map[string]any{{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": content,
			},
			"finish_reason": "stop",
		}},
	})
}
