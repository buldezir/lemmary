package config

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"lemmary/backend/internal/aiprovider"
)

// NewRuntime is the only production write of the managed flag, and every other
// test sets it by hand: without this one, deleting the SetManaged call would
// leave managed installs sending no header and the suite would still pass.
func TestNewRuntimeHandsManagedToAIProvider(t *testing.T) {
	prev := aiprovider.Managed()
	t.Cleanup(func() { aiprovider.SetManaged(prev) })

	for _, tc := range []struct {
		name    string
		managed bool
		want    string
	}{
		{"managed", true, "doc123"},
		{"self-hosted", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Set to the opposite first, so a NewRuntime that wrote nothing at all would
			// fail rather than inherit the answer.
			aiprovider.SetManaged(!tc.managed)
			NewRuntime(AIEnv{Managed: tc.managed})

			if got := aiprovider.Managed(); got != tc.managed {
				t.Fatalf("aiprovider.Managed() = %v, want %v", got, tc.managed)
			}
			if opts := aiprovider.DocumentOptions(); (len(opts) > 0) != tc.managed {
				t.Errorf("DocumentOptions() has %d middleware(s), managed = %v", len(opts), tc.managed)
			}

			req := httptest.NewRequest(http.MethodPost, "http://example.test/v1/chat/completions", nil)
			req = req.WithContext(aiprovider.WithDocument(context.Background(), "doc123"))
			aiprovider.StampDocument(req)
			if got := req.Header.Get(aiprovider.DocumentHeader); got != tc.want {
				t.Errorf("%s = %q, want %q", aiprovider.DocumentHeader, got, tc.want)
			}
		})
	}
}
