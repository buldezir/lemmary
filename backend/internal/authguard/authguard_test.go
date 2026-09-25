package authguard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestIsCollectionRecordRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		want bool
	}{
		{name: "list records", path: "/api/collections/documents/records", want: true},
		{name: "list records trailing slash", path: "/api/collections/documents/records/", want: true},
		{name: "view record", path: "/api/collections/documents/records/abc123", want: true},
		{name: "auth endpoint", path: "/api/collections/users/auth-with-password", want: false},
		{name: "collection metadata", path: "/api/collections", want: false},
		{name: "custom api", path: "/api/documents/", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			if got := isCollectionRecordRequest(req); got != tt.want {
				t.Fatalf("isCollectionRecordRequest(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestExtractAuthToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		header string
		want   string
	}{
		{name: "raw token", header: "abc.def.ghi", want: "abc.def.ghi"},
		{name: "bearer token", header: "Bearer abc.def.ghi", want: "abc.def.ghi"},
		{name: "lower bearer token", header: "bearer abc.def.ghi", want: "abc.def.ghi"},
		{name: "paperless token", header: "Token abc.def.ghi", want: "abc.def.ghi"},
		{name: "trims whitespace", header: "  Bearer abc.def.ghi  ", want: "abc.def.ghi"},
		{name: "empty", header: " ", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := extractAuthToken(tt.header); got != tt.want {
				t.Fatalf("extractAuthToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestRequireCollectionRecordsAuthRejectsMissingAuth(t *testing.T) {
	t.Parallel()

	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest(http.MethodGet, "/api/collections/documents/records", nil)
	e.Response = httptest.NewRecorder()

	err := requireCollectionRecordsAuth(e)
	if err == nil {
		t.Fatal("requireCollectionRecordsAuth() error = nil, want 401")
	}

	apiErr := &router.ApiError{}
	ok := errors.As(err, &apiErr)
	if !ok {
		t.Fatalf("error type = %T, want *router.ApiError", err)
	}
	if apiErr.Status != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", apiErr.Status, http.StatusUnauthorized)
	}
}

func TestRequireCollectionRecordsAuthSkipsAuthEndpoints(t *testing.T) {
	t.Parallel()

	e := &core.RequestEvent{}
	e.Request = httptest.NewRequest(http.MethodPost, "/api/collections/users/auth-with-password", nil)
	e.Response = httptest.NewRecorder()

	if err := requireCollectionRecordsAuth(e); err != nil {
		t.Fatalf("requireCollectionRecordsAuth() error = %v, want nil", err)
	}
}
