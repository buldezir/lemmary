package ngxapi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// An upload naming a relation that does not exist is refused, as PATCH and
// paperless-ngx refuse it, rather than stored without that relation.
func TestPostDocumentRefusesUnknownRelation(t *testing.T) {
	f := newListFixture(t)
	user, err := f.app.FindRecordById("users", f.userID)
	if err != nil {
		t.Fatalf("load auth user: %v", err)
	}
	before, err := f.app.CountRecords("documents", dbx.HashExp{"user": f.userID})
	if err != nil {
		t.Fatalf("count documents: %v", err)
	}

	for _, field := range []string{"correspondent", "document_type"} {
		var body bytes.Buffer
		w := multipart.NewWriter(&body)
		if err := w.WriteField(field, "987654321"); err != nil {
			t.Fatal(err)
		}
		part, err := w.CreateFormFile("document", field+".txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte("upload with unknown " + field)); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}

		e := &core.RequestEvent{}
		e.App = f.app
		e.Auth = user
		e.Request = httptest.NewRequest(http.MethodPost, "/api/documents/post_document/", &body)
		e.Request.Header.Set("Content-Type", w.FormDataContentType())
		e.Response = httptest.NewRecorder()

		if err := handlePostDocument(e); err != nil {
			t.Fatalf("%s: handler: %v", field, err)
		}
		rec := e.Response.(*httptest.ResponseRecorder)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "unknown "+field+" id") {
			t.Fatalf("%s: status %d body %s, want 400 naming the unknown id", field, rec.Code, rec.Body.String())
		}
	}

	after, err := f.app.CountRecords("documents", dbx.HashExp{"user": f.userID})
	if err != nil {
		t.Fatalf("count documents: %v", err)
	}
	if after != before {
		t.Fatalf("documents went from %d to %d, want no document created", before, after)
	}
}
