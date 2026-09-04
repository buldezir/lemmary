package ocr

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// writeTempFile puts bytes on disk under name, because ExtractText takes a path.
func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

type doclingCapture struct {
	calls    atomic.Int64
	path     string
	method   string
	apiKey   string
	fields   map[string][]string
	fileName string
	fileMIME string
	fileData string
}

// newDoclingServer answers every convert with body, recording what it was sent.
func newDoclingServer(t *testing.T, status int, body string) (*httptest.Server, *doclingCapture) {
	t.Helper()
	capture := &doclingCapture{fields: map[string][]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.calls.Add(1)
		capture.path = r.URL.Path
		capture.method = r.Method
		capture.apiKey = r.Header.Get("X-Api-Key")

		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for name, values := range r.MultipartForm.Value {
			capture.fields[name] = values
		}
		if parts := r.MultipartForm.File["files"]; len(parts) == 1 {
			capture.fileName = parts[0].Filename
			capture.fileMIME = parts[0].Header.Get("Content-Type")
			file, err := parts[0].Open()
			if err != nil {
				t.Errorf("open file part: %v", err)
			} else {
				defer file.Close()
				data, _ := io.ReadAll(file)
				capture.fileData = string(data)
			}
		} else {
			t.Errorf("want exactly one files part, got %d", len(parts))
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, capture
}

func doclingBody(md string) string {
	out, err := json.Marshal(doclingResponse{
		Document: doclingDocument{MDContent: md},
		Status:   "success",
	})
	if err != nil {
		panic(err)
	}
	return string(out)
}

func TestDoclingSendsTheDocumentAndReturnsItsMarkdown(t *testing.T) {
	server, capture := newDoclingServer(t, http.StatusOK, doclingBody("# Invoice\n\nTotal: 12.00"))
	provider := NewDoclingProvider(server.URL, "", "", 5*time.Second, nil)

	path := writeTempFile(t, "lemmary-doc-123.pdf", []byte("%PDF-1.7 fake"))
	text, err := provider.ExtractText(context.Background(), path, "application/pdf")
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if text != "# Invoice\n\nTotal: 12.00" {
		t.Errorf("text = %q", text)
	}

	if capture.method != http.MethodPost || capture.path != "/v1/convert/file" {
		t.Errorf("request = %s %s, want POST /v1/convert/file", capture.method, capture.path)
	}
	// The generated upload name carries the extension, but docling reads the
	// part's content type first -- so both have to arrive intact.
	if capture.fileName != "lemmary-doc-123.pdf" {
		t.Errorf("file name = %q", capture.fileName)
	}
	if capture.fileMIME != "application/pdf" {
		t.Errorf("file content type = %q, want application/pdf", capture.fileMIME)
	}
	if capture.fileData != "%PDF-1.7 fake" {
		t.Errorf("file body = %q", capture.fileData)
	}
	for field, want := range map[string]string{
		"to_formats":        "md",
		"do_ocr":            "true",
		"image_export_mode": "placeholder",
	} {
		got := capture.fields[field]
		if len(got) != 1 || got[0] != want {
			t.Errorf("field %s = %v, want [%s]", field, got, want)
		}
	}
	// Left to the server's default unless an admin bound an engine.
	if _, sent := capture.fields["ocr_engine"]; sent {
		t.Errorf("ocr_engine sent with no engine bound: %v", capture.fields["ocr_engine"])
	}
}

func TestDoclingSendsABoundEngine(t *testing.T) {
	server, capture := newDoclingServer(t, http.StatusOK, doclingBody("text"))
	provider := NewDoclingProvider(server.URL, "tesseract", "", 5*time.Second, nil)

	path := writeTempFile(t, "scan.png", []byte("PNG"))
	if _, err := provider.ExtractText(context.Background(), path, "image/png"); err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if got := capture.fields["ocr_engine"]; len(got) != 1 || got[0] != "tesseract" {
		t.Errorf("ocr_engine = %v, want [tesseract]", got)
	}
}

func TestDoclingSendsTheAPIKeyOnlyWhenThereIsOne(t *testing.T) {
	for _, tc := range []struct{ name, key, want string }{
		{"keyless", "", ""},
		{"with key", "s3cret", "s3cret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, capture := newDoclingServer(t, http.StatusOK, doclingBody("text"))
			provider := NewDoclingProvider(server.URL, "", tc.key, 5*time.Second, nil)

			path := writeTempFile(t, "doc.pdf", []byte("%PDF"))
			if _, err := provider.ExtractText(context.Background(), path, "application/pdf"); err != nil {
				t.Fatalf("ExtractText: %v", err)
			}
			if capture.apiKey != tc.want {
				t.Errorf("X-Api-Key = %q, want %q", capture.apiKey, tc.want)
			}
		})
	}
}

func TestDoclingErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{
			name:    "server error carries the body",
			status:  http.StatusInternalServerError,
			body:    `{"detail":"model not loaded"}`,
			wantSub: "model not loaded",
		},
		{
			// A per-document failure answers 200 with an empty md_content and a
			// populated errors array; without surfacing it the operator sees
			// only "empty text" for, say, a password-protected PDF.
			name:    "empty markdown reports the document error",
			status:  http.StatusOK,
			body:    `{"document":{"md_content":""},"status":"failure","errors":[{"module_name":"pdf","error_message":"encrypted"}]}`,
			wantSub: "pdf: encrypted",
		},
		{
			name:    "errors may be bare strings",
			status:  http.StatusOK,
			body:    `{"document":{"md_content":""},"status":"failure","errors":["unsupported"]}`,
			wantSub: "unsupported",
		},
		{
			name:    "empty markdown with no detail still fails",
			status:  http.StatusOK,
			body:    `{"document":{"md_content":"   "},"status":"success","errors":[]}`,
			wantSub: "empty text",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newDoclingServer(t, tc.status, tc.body)
			provider := NewDoclingProvider(server.URL, "", "", 5*time.Second, nil)

			path := writeTempFile(t, "doc.pdf", []byte("%PDF"))
			_, err := provider.ExtractText(context.Background(), path, "application/pdf")
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantSub)
			}
		})
	}
}

func TestDoclingRefusesUnsupportedMimeWithoutCallingTheSidecar(t *testing.T) {
	server, capture := newDoclingServer(t, http.StatusOK, doclingBody("text"))
	provider := NewDoclingProvider(server.URL, "", "", 5*time.Second, nil)

	for _, name := range []string{"archive.zip", "photo.avif"} {
		path := writeTempFile(t, name, []byte("data"))
		if _, err := provider.ExtractText(context.Background(), path, ""); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if calls := capture.calls.Load(); calls != 0 {
		t.Errorf("sidecar called %d times for unsupported input", calls)
	}
}

func TestDoclingSerializesRequests(t *testing.T) {
	// The split detector fans out across pages; a local sidecar shares one
	// host's CPUs, so it says one at a time and pdfsplit honours that.
	if got := NewDoclingProvider("http://docling:5001", "", "", 0, nil).MaxConcurrency(); got != 1 {
		t.Errorf("MaxConcurrency = %d, want 1", got)
	}
}
