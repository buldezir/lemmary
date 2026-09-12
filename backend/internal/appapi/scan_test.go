package appapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/escl"
	"lemmary/backend/internal/limits"
)

func scanEvent(method, target string, body string) (*core.RequestEvent, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Response = rec
	var reader *strings.Reader
	if body != "" {
		reader = strings.NewReader(body)
	} else {
		reader = strings.NewReader("")
	}
	e.Request = httptest.NewRequest(method, target, reader)
	return e, rec
}

// The sweep makes the server issue requests on the caller's behalf, so a range
// it should not touch has to be refused before anything is dialled -- which is
// also why a nil app is safe here.
func TestScanDiscoverRefusesARangeOffTheLocalNetwork(t *testing.T) {
	t.Parallel()

	e, rec := scanEvent(http.MethodGet, "/api/app/scan/discover?cidr=8.8.8.0/24", "")
	if err := handleGetScanDiscover(nil)(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("the refusal should say why: %q", rec.Body.String())
	}
}

func TestScanStatusNeedsAJobID(t *testing.T) {
	t.Parallel()

	e, rec := scanEvent(http.MethodGet, "/api/app/scan/status", "")
	if err := handleGetScanStatus(nil)(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestScanRejectsAnUnreadableBody(t *testing.T) {
	t.Parallel()

	e, rec := scanEvent(http.MethodPost, "/api/app/scan", "{")
	if err := handlePostScan(nil, limits.Limits{})(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusBadRequest)
	}
}

// Saving a scan has to fail the way saving a file fails: the Scan tab shows the
// same messages, and the duplicate link is what makes "already in your library"
// useful rather than annoying.
func TestScanSaveErrorsReadLikeAnUploadsDo(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantBody   string
	}{
		{
			name:       "expired",
			err:        escl.ErrUploadNotFound,
			wantStatus: http.StatusNotFound,
			wantBody:   "expired",
		},
		{
			name:       "too large",
			err:        escl.ErrTooLarge,
			wantStatus: http.StatusBadRequest,
			wantBody:   "limit",
		},
		{
			name:       "duplicate",
			err:        &duplicates.ErrDuplicate{ExistingID: "doc123"},
			wantStatus: http.StatusBadRequest,
			wantBody:   "doc123",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, rec := scanEvent(http.MethodPost, "/api/app/scan/document", "")
			if err := writeScanSaveError(nil, e, tc.err); err != nil {
				t.Fatalf("writeScanSaveError: %v", err)
			}
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d want %d", rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body=%q want it to mention %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}
