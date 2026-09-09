package aiprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSessionForIsStablePerPurpose(t *testing.T) {
	a := SessionFor("extract")
	if a == "" {
		t.Fatal("SessionFor returned an empty id")
	}
	if b := SessionFor("extract"); b != a {
		t.Errorf("SessionFor(%q) is not stable: %q then %q", "extract", a, b)
	}
	if other := SessionFor("ocr"); other == a {
		t.Errorf("SessionFor(\"ocr\") = SessionFor(\"extract\") = %q; purposes must not share a key", other)
	}
	if blank := SessionFor("  "); blank != processSession {
		t.Errorf("SessionFor(blank) = %q, want the process id %q", blank, processSession)
	}
}

func TestWithSessionIgnoresABlankID(t *testing.T) {
	ctx := WithSession(context.Background(), "  ")
	if got := SessionFrom(ctx); got != "" {
		t.Errorf("SessionFrom = %q, want empty", got)
	}
	if got := SessionFrom(nil); got != "" {
		t.Errorf("SessionFrom(nil) = %q, want empty", got)
	}
	if got := SessionFrom(WithSession(context.Background(), " abc ")); got != "abc" {
		t.Errorf("SessionFrom = %q, want %q", got, "abc")
	}
}

func TestEnsureSessionKeepsTheConversation(t *testing.T) {
	ctx := EnsureSession(WithSession(context.Background(), "conv123"), "chat")
	if got := SessionFrom(ctx); got != "conv123" {
		t.Errorf("EnsureSession overwrote the conversation: got %q", got)
	}

	filled := EnsureSession(context.Background(), "chat")
	if got, want := SessionFrom(filled), SessionFor("chat"); got != want {
		t.Errorf("EnsureSession = %q, want the purpose fallback %q", got, want)
	}
}

func TestSessionMiddlewareStampsTheRequest(t *testing.T) {
	mw := SessionMiddleware()

	req := httptest.NewRequest(http.MethodPost, "https://opencode.ai/zen/go/v1/chat/completions", nil)
	req = req.WithContext(WithSession(req.Context(), "conv123"))

	var seen string
	_, err := mw(req, func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Get(SessionHeader)
		return &http.Response{StatusCode: http.StatusOK}, nil
	})
	if err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if seen != "conv123" {
		t.Errorf("%s = %q, want %q", SessionHeader, seen, "conv123")
	}
}

// Which providers see the header is now the caller's decision -- only an
// SDKOpenCode client installs this middleware -- so the middleware itself no
// longer looks at the URL. That gate used to be a host match on opencode.ai,
// which meant a test could only exercise it by rewriting the host underneath.
func TestSessionMiddlewareDoesNotLookAtTheHost(t *testing.T) {
	mw := SessionMiddleware()

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9/v1/chat/completions", nil)
	req = req.WithContext(WithSession(req.Context(), "conv123"))

	var seen string
	if _, err := mw(req, func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Get(SessionHeader)
		return &http.Response{StatusCode: http.StatusOK}, nil
	}); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if seen != "conv123" {
		t.Errorf("%s = %q, want it stamped whatever the host", SessionHeader, seen)
	}
}

func TestSessionMiddlewareSendsNothingWithoutASession(t *testing.T) {
	mw := SessionMiddleware()
	req := httptest.NewRequest(http.MethodPost, "https://opencode.ai/zen/go/v1/chat/completions", nil)

	var seen string
	if _, err := mw(req, func(r *http.Request) (*http.Response, error) {
		seen = r.Header.Get(SessionHeader)
		return &http.Response{StatusCode: http.StatusOK}, nil
	}); err != nil {
		t.Fatalf("middleware: %v", err)
	}
	if seen != "" {
		t.Errorf("%s = %q, want empty when the context names no conversation", SessionHeader, seen)
	}
}
