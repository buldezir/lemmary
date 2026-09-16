package ai

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
)

func apiError(status int, message string) error {
	return &openai.Error{
		StatusCode: status,
		Message:    message,
		Request:    &http.Request{},
		Response:   &http.Response{StatusCode: status},
	}
}

func TestProviderErrorMessageNamesTheStatusAndTheReason(t *testing.T) {
	t.Parallel()
	got := ProviderErrorMessage(apiError(http.StatusBadRequest,
		"This model's maximum context length is 128000 tokens."))
	if !strings.Contains(got, "400") {
		t.Fatalf("message lost the status: %q", got)
	}
	if !strings.Contains(got, "maximum context length") {
		t.Fatalf("message lost the provider's reason: %q", got)
	}
}

// Wrapped is how it actually arrives: the research loop puts its own sentence
// in front of the provider's.
func TestProviderErrorMessageUnwraps(t *testing.T) {
	t.Parallel()
	wrapped := errors.Join(errors.New("openai research completion"), apiError(429, "Rate limit reached."))
	got := ProviderErrorMessage(wrapped)
	if !strings.Contains(got, "429") || !strings.Contains(got, "Rate limit reached.") {
		t.Fatalf("wrapped provider error did not survive: %q", got)
	}
}

func TestProviderErrorMessageRedactsCredentialsAndAddresses(t *testing.T) {
	t.Parallel()
	got := ProviderErrorMessage(apiError(401,
		"Incorrect API key sk-proj-abcdef1234567890 provided to https://api.example.com/v1 with Bearer tok_abc123"))

	for _, secret := range []string{"sk-proj-abcdef1234567890", "https://api.example.com/v1", "tok_abc123"} {
		if strings.Contains(got, secret) {
			t.Fatalf("message leaked %q: %s", secret, got)
		}
	}
	if !strings.Contains(got, "[redacted]") {
		t.Fatalf("nothing was redacted: %q", got)
	}
	if !strings.Contains(got, "Incorrect API key") {
		t.Fatalf("redaction ate the reason: %q", got)
	}
}

func TestProviderErrorMessageBoundsItsLength(t *testing.T) {
	t.Parallel()
	got := ProviderErrorMessage(apiError(500, strings.Repeat("verbose ", 500)))
	if len([]rune(got)) > maxProviderErrorRunes+len("Provider error (500): ")+1 {
		t.Fatalf("message not bounded: %d runes", len([]rune(got)))
	}
}

func TestProviderErrorMessageHandlesPlainErrors(t *testing.T) {
	t.Parallel()
	if got := ProviderErrorMessage(nil); got != "" {
		t.Fatalf("nil should render nothing, got %q", got)
	}
	got := ProviderErrorMessage(errors.New("dial tcp: connection refused"))
	if !strings.Contains(got, "connection refused") {
		t.Fatalf("a non-SDK error should still reach the user: %q", got)
	}
}

// A transport failure never reaches an API type, so it arrives with the address
// it could not reach -- which is where the operator's model lives.
func TestProviderErrorMessageRedactsAddressesInTransportErrors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		err    error
		secret string
	}{
		{"ipv4 and port", errors.New("dial tcp 10.0.0.5:11434: connect: connection refused"), "10.0.0.5"},
		{"hostname and port", errors.New("dial tcp ollama.internal:11434: i/o timeout"), "ollama.internal:11434"},
		{"localhost and port", errors.New("dial tcp localhost:8080: connection refused"), "localhost:8080"},
		{"ipv6 and port", errors.New("dial tcp [fd00::1]:11434: no route to host"), "fd00::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ProviderErrorMessage(tc.err)
			if strings.Contains(got, tc.secret) {
				t.Fatalf("message leaked %q: %s", tc.secret, got)
			}
			// Redacting the address must not cost the diagnosis.
			if !strings.Contains(got, "dial tcp") {
				t.Fatalf("redaction ate the reason: %q", got)
			}
		})
	}
}

// Only an address, not every dotted name: a vendor named in prose carries no
// port and is the part of the message worth reading.
func TestProviderErrorMessageKeepsHostnamesWithoutAPort(t *testing.T) {
	t.Parallel()
	got := ProviderErrorMessage(apiError(404, "The model gpt-4.1-mini does not exist for api.openai.com accounts."))
	if !strings.Contains(got, "gpt-4.1-mini") {
		t.Fatalf("a model id was redacted as an address: %q", got)
	}
	if !strings.Contains(got, "api.openai.com") {
		t.Fatalf("a bare hostname was redacted: %q", got)
	}
}
