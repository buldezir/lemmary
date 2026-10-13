package ngxapi

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// Paperparrot decodes every key; a missing one failed the whole response and
// the document's suggestions with it.
func TestSuggestionsCarryEveryKeyAClientDecodes(t *testing.T) {
	f := newListFixture(t)
	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodGet, "/api/documents/x/suggestions/", nil)
	e.Request.SetPathValue("id", fmt.Sprint(storedID(t, f.app, "documents", f.docOne)))
	e.Response = httptest.NewRecorder()
	e.Auth = mustFind(t, f.app, "users", f.userID)

	if err := handleDocumentSuggestions(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	var body map[string][]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); rec.Code != http.StatusOK || err != nil {
		t.Fatalf("status %d, %v: %s", rec.Code, err, rec.Body.String())
	}
	for _, key := range []string{"correspondents", "tags", "document_types", "storage_paths", "dates"} {
		if list, ok := body[key]; !ok || list == nil {
			t.Fatalf("%s missing or null in %s", key, rec.Body.String())
		}
	}

	// Change rights, as in paperless: another account gets nothing.
	e.Response = httptest.NewRecorder()
	e.Auth = mustFind(t, f.app, "users", f.otherID)
	if err := handleDocumentSuggestions(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if code := e.Response.(*httptest.ResponseRecorder).Code; code != http.StatusNotFound {
		t.Fatalf("another account got %d, want 404", code)
	}
}
