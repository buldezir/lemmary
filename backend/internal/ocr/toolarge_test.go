package ocr

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
)

func TestTooLarge(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{errors.New(`llm ocr: POST "/chat/completions": 429 Too Many Requests {"message":"request rejected: your request would exceed the configured rate limit of 950000 cached and uncached prompt tokens per minute"}`), true},
		{fmt.Errorf("ocr: %w", &HTTPError{Status: http.StatusRequestEntityTooLarge, msg: "mistral OCR: payload"}), true},
		{&HTTPError{Status: http.StatusTooManyRequests, msg: "mistral OCR: slow down"}, true},
		{fmt.Errorf("llm ocr: %w", errors.New("This model's maximum context length is 128000 tokens")), true},
		{errors.New("anthropic: prompt is too long: 250000 tokens > 200000 maximum"), true},
		{errors.New(`429 Too Many Requests {"code":"insufficient_quota","message":"You exceeded your current quota"}`), false},
		{errors.New("provider \"x\" has no API key"), false},
		{context.DeadlineExceeded, false},
	} {
		if got := TooLarge(tc.err); got != tc.want {
			t.Errorf("TooLarge(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()

	withHeader := &openai.Error{StatusCode: http.StatusTooManyRequests, Response: &http.Response{Header: http.Header{"Retry-After": {"7"}}}}
	if wait, ok := RetryAfter(fmt.Errorf("llm ocr: %w", withHeader)); !ok || wait != 7*time.Second {
		t.Errorf("Retry-After 7 = %v, %v; want 7s, true", wait, ok)
	}
	if wait, ok := RetryAfter(&HTTPError{Status: http.StatusTooManyRequests, RetryAfter: "3", msg: "mistral OCR: slow down"}); !ok || wait != 3*time.Second {
		t.Errorf("mistral Retry-After 3 = %v, %v; want 3s, true", wait, ok)
	}
	if wait, ok := RetryAfter(errors.New("upstream: rate limit reached")); !ok || wait != time.Minute {
		t.Errorf("no header = %v, %v; want 1m, true", wait, ok)
	}
	if _, ok := RetryAfter(errors.New("This model's maximum context length is 128000 tokens")); ok {
		t.Error("a context-length refusal is not a rate limit")
	}
	// As openai.Error prints it: method, URL, status and the raw body.
	tooLargeForTPM := fmt.Errorf("llm ocr: %w", errors.New(`POST "https://api.openai.com/v1/chat/completions": 429 Too Many Requests {"error":{"message":"Request too large for gpt-4o on tokens per min (TPM): Limit 30000, Requested 50000.","type":"tokens","code":"rate_limit_exceeded"}}`))
	if _, ok := RetryAfter(tooLargeForTPM); ok {
		t.Error("a request too large for the per-minute limit must not wait")
	}
	if !TooLarge(tooLargeForTPM) {
		t.Error("a request too large for the per-minute limit must still split")
	}
}
