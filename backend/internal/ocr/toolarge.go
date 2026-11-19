package ocr

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go/v3"
)

// tooLargePhrases are matched against the message, because Mistral and docling
// return plain errors, and an OpenAI-compatible gateway reports an overlong
// prompt as a 400.
var tooLargePhrases = []string{
	"too many tokens",
	"too large",
	"prompt is too long",
	"context length",
	"context_length",
	"context window",
	"maximum context",
}

var rateLimitPhrases = []string{"rate limit", "too many requests"}

// A spent quota or balance is also a 429, but no smaller request will pass it.
var quotaPhrases = []string{"quota", "billing", "insufficient", "credit"}

const (
	defaultRateLimitWait = time.Minute
	maxRateLimitWait     = 5 * time.Minute
)

// TooLarge reports whether a provider refused a document for its size or its
// rate limit, so a smaller part of the same document may succeed.
func TooLarge(err error) bool {
	if err == nil || containsAny(err, quotaPhrases) {
		return false
	}
	status, _ := apiError(err)
	_, limited := RetryAfter(err)
	return limited || status == http.StatusRequestEntityTooLarge || containsAny(err, tooLargePhrases)
}

// RetryAfter reports whether err is a rate-limit refusal, and how long to wait
// before the next request: the provider's Retry-After, else a minute, which is
// the window per-minute limits count in. A 429 worded as too large (OpenAI's
// "Request too large ... on tokens per min") is not one: that request exceeds
// the limit on its own, and waiting will not let it pass.
func RetryAfter(err error) (time.Duration, bool) {
	if err == nil || containsAny(err, quotaPhrases) || containsAny(err, tooLargePhrases) {
		return 0, false
	}
	status, retryAfter := apiError(err)
	if status != http.StatusTooManyRequests && !containsAny(err, rateLimitPhrases) {
		return 0, false
	}
	if secs, parseErr := strconv.Atoi(strings.TrimSpace(retryAfter)); parseErr == nil && secs >= 0 {
		return min(time.Duration(secs)*time.Second, maxRateLimitWait), true
	}
	return defaultRateLimitWait, true
}

// HTTPError is a provider's non-2xx answer, keeping the status and
// Retry-After that TooLarge and RetryAfter read.
type HTTPError struct {
	Status     int
	RetryAfter string
	msg        string
}

func (e *HTTPError) Error() string { return e.msg }

func newHTTPError(resp *http.Response, format string, args ...any) error {
	return &HTTPError{Status: resp.StatusCode, RetryAfter: resp.Header.Get("Retry-After"), msg: fmt.Sprintf(format, args...)}
}

// apiError returns the HTTP status and Retry-After header of a provider error,
// whichever client returned it.
func apiError(err error) (status int, retryAfter string) {
	var resp *http.Response
	if apiErr, ok := errors.AsType[*openai.Error](err); ok {
		status, resp = apiErr.StatusCode, apiErr.Response
	} else if apiErr, ok := errors.AsType[*anthropic.Error](err); ok {
		status, resp = apiErr.StatusCode, apiErr.Response
	} else if httpErr, ok := errors.AsType[*HTTPError](err); ok {
		return httpErr.Status, httpErr.RetryAfter
	}
	if resp != nil {
		retryAfter = resp.Header.Get("Retry-After")
	}
	return status, retryAfter
}

func containsAny(err error, phrases []string) bool {
	msg := strings.ToLower(err.Error())
	for _, phrase := range phrases {
		if strings.Contains(msg, phrase) {
			return true
		}
	}
	return false
}
