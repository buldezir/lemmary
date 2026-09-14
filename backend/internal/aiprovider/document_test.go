package aiprovider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// managedForTest turns managed mode on for the length of one test. Not
// parallel-safe, which is why no test here calls t.Parallel().
func managedForTest(t *testing.T, on bool) {
	t.Helper()
	prev := Managed()
	SetManaged(on)
	t.Cleanup(func() { SetManaged(prev) })
}

func TestWithDocumentRoundTrip(t *testing.T) {
	ctx := WithDocument(context.Background(), "doc123")
	if got := DocumentFrom(ctx); got != "doc123" {
		t.Errorf("DocumentFrom = %q, want %q", got, "doc123")
	}
}

func TestWithDocumentTrimsAndIgnoresEmpty(t *testing.T) {
	if got := DocumentFrom(WithDocument(context.Background(), "  doc123\n")); got != "doc123" {
		t.Errorf("DocumentFrom = %q, want %q", got, "doc123")
	}
	// An id that is only whitespace leaves the context alone rather than
	// storing "": a header with an empty value is worse than no header.
	ctx := WithDocument(WithDocument(context.Background(), "doc123"), "   ")
	if got := DocumentFrom(ctx); got != "doc123" {
		t.Errorf("blank id replaced the document: %q", got)
	}
	if got := DocumentFrom(context.Background()); got != "" {
		t.Errorf("DocumentFrom on a bare context = %q, want empty", got)
	}
	if got := DocumentFrom(nil); got != "" { //nolint:staticcheck // a nil context is what a careless caller passes
		t.Errorf("DocumentFrom(nil) = %q, want empty", got)
	}
}

func TestDocumentOptionsOnlyWhenManaged(t *testing.T) {
	managedForTest(t, false)
	if opts := DocumentOptions(); len(opts) != 0 {
		t.Errorf("self-hosted install installed %d middleware(s)", len(opts))
	}
	SetManaged(true)
	if opts := DocumentOptions(); len(opts) != 1 {
		t.Errorf("managed install got %d middleware(s), want 1", len(opts))
	}
}

func TestStampDocument(t *testing.T) {
	cases := []struct {
		name    string
		managed bool
		doc     string
		want    string
	}{
		{"managed with a document", true, "doc123", "doc123"},
		{"managed without a document", true, "", ""},
		{"self-hosted with a document", false, "doc123", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			managedForTest(t, tc.managed)
			req := httptest.NewRequest(http.MethodPost, "http://example.test/v1/chat/completions", nil)
			req = req.WithContext(WithDocument(req.Context(), tc.doc))
			StampDocument(req)
			if got := req.Header.Get(DocumentHeader); got != tc.want {
				t.Errorf("%s = %q, want %q", DocumentHeader, got, tc.want)
			}
		})
	}
}

// The header's value is the checksum: the record id is what the last mistake
// put there, and it is the field sitting right next to it.
func TestWithDocumentRecordCarriesChecksumNotID(t *testing.T) {
	documents := core.NewBaseCollection("documents")
	documents.Fields.Add(&core.TextField{Name: "checksum"})
	record := core.NewRecord(documents)
	record.Id = "doc123"
	record.Set("checksum", "a1b2c3")

	if got := DocumentFrom(WithDocumentRecord(context.Background(), record)); got != "a1b2c3" {
		t.Errorf("DocumentFrom = %q, want the checksum", got)
	}

	// A duplicate gives its checksum up to the original, and a file that has
	// not been hashed yet has none: both go out unnamed, not under the id.
	record.Set("checksum", "")
	if got := DocumentFrom(WithDocumentRecord(context.Background(), record)); got != "" {
		t.Errorf("DocumentFrom with no checksum = %q, want empty", got)
	}
	if got := DocumentFrom(WithDocumentRecord(context.Background(), nil)); got != "" {
		t.Errorf("DocumentFrom with no record = %q, want empty", got)
	}
}
