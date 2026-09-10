package appwire

import (
	"net/http"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

// The 401 rows are the reason this middleware is hand-rolled. PocketBase
// writes an error response in router.ErrorHandler after the handler chain has
// unwound, so at the moment a hook on that chain sees the request nothing has
// been written and any wrapper watching the ResponseWriter reads it as 200 --
// which would have every refused request scraped as a healthy one.
func TestResponseStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		written int
		err     error
		want    int
	}{
		{
			name: "unauthorized returned by a handler, nothing written",
			err:  router.NewUnauthorizedError("", nil),
			want: http.StatusUnauthorized,
		},
		{
			name: "not found returned by a handler",
			err:  router.NewNotFoundError("", nil),
			want: http.StatusNotFound,
		},
		{
			name: "bad request returned by a handler",
			err:  router.NewBadRequestError("", nil),
			want: http.StatusBadRequest,
		},
		{
			name: "an error that is not an ApiError still maps the way ErrorHandler will",
			err:  errPlain{},
			want: router.ToApiError(errPlain{}).Status,
		},
		{
			name:    "a written status wins when there is no error",
			written: http.StatusCreated,
			want:    http.StatusCreated,
		},
		{
			name:    "an error outranks whatever was written, since ErrorHandler is what answers",
			written: http.StatusOK,
			err:     router.NewUnauthorizedError("", nil),
			want:    http.StatusUnauthorized,
		},
		{
			name: "nothing written and no error is net/http's own default",
			want: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			event := &core.RequestEvent{}
			event.Response = &statusWriter{status: tc.written}
			if got := responseStatus(event, tc.err); got != tc.want {
				t.Errorf("responseStatus = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRouteOf(t *testing.T) {
	t.Parallel()

	// PocketBase registers patterns with the method on the front, and the
	// method is already an attribute of its own.
	cases := map[string]string{
		"GET /api/collections/{collection}/records": "/api/collections/{collection}/records",
		"POST /api/app/documents":                   "/api/app/documents",
		"/{path...}":                                "/{path...}",
		// Nothing matched, so there is no route to name -- and the raw URL
		// must never become the label, or every id in the archive is a series.
		"": "unmatched",
	}
	for pattern, want := range cases {
		got := routeOf(&http.Request{Pattern: pattern})
		if got != want {
			t.Errorf("routeOf(%q) = %q, want %q", pattern, got, want)
		}
	}
}

type errPlain struct{}

func (errPlain) Error() string { return "something went wrong" }

// statusWriter is the StatusTracker core.RequestEvent.Status reads through. A
// zero status is "nothing written yet", which is what the router's own
// ResponseWriter reports before a handler answers.
type statusWriter struct {
	status int
}

func (w *statusWriter) Header() http.Header { return http.Header{} }
func (w *statusWriter) Write(b []byte) (int, error) {
	return len(b), nil
}
func (w *statusWriter) WriteHeader(status int) { w.status = status }
func (w *statusWriter) Status() int            { return w.status }
