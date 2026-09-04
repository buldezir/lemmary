package ocr

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type paddleCapture struct {
	calls    atomic.Int64
	path     string
	file     string
	fileType int
}

func newPaddleServer(t *testing.T, status int, body string) (*httptest.Server, *paddleCapture) {
	t.Helper()
	capture := &paddleCapture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture.calls.Add(1)
		capture.path = r.URL.Path

		var req paddleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		capture.file = req.File
		capture.fileType = req.FileType

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, capture
}

// TestPaddleOCRReadsEveryResponseShape is the point of the defensive parser.
// PaddleX has spelled the same answer four ways across releases, and a struct
// that knew only the current one would decode cleanly and yield "" -- an
// archive full of empty documents rather than a failure anybody would notice.
func TestPaddleOCRReadsEveryResponseShape(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "3.x markdown object",
			body: `{"errorCode":0,"result":{"layoutParsingResults":[{"markdown":{"text":"# Page one"}}]}}`,
			want: "# Page one",
		},
		{
			name: "markdown as a bare string",
			body: `{"errorCode":0,"result":{"layoutParsingResults":[{"markdown":"# Page one"}]}}`,
			want: "# Page one",
		},
		{
			name: "beta layout elements",
			body: `{"errorCode":0,"result":{"layoutParsingResults":[{"layoutElements":[{"text":"Title"},{"text":"Body"}]}]}}`,
			want: "Title\n\nBody",
		},
		{
			name: "layout result falling back to recognised lines",
			body: `{"errorCode":0,"result":{"layoutParsingResults":[{"prunedResult":{"rec_texts":["one","two"]}}]}}`,
			want: "one\ntwo",
		},
		{
			name: "the plain ocr pipeline",
			body: `{"errorCode":0,"result":{"ocrResults":[{"prunedResult":{"rec_texts":["one","two"]}}]}}`,
			want: "one\ntwo",
		},
		{
			name: "pages are joined in order",
			body: `{"errorCode":0,"result":{"layoutParsingResults":[{"markdown":{"text":"one"}},{"markdown":{"text":"two"}}]}}`,
			want: "one\n\ntwo",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newPaddleServer(t, http.StatusOK, tc.body)
			provider := NewPaddleOCRProvider(server.URL, "", 5*time.Second, nil)

			path := writeTempFile(t, "doc.pdf", []byte("%PDF"))
			text, err := provider.ExtractText(context.Background(), path, "application/pdf")
			if err != nil {
				t.Fatalf("ExtractText: %v", err)
			}
			if text != tc.want {
				t.Errorf("text = %q, want %q", text, tc.want)
			}
		})
	}
}

func TestPaddleOCRSendsBareBase64AndTheRightFileType(t *testing.T) {
	tests := []struct {
		name     string
		fileName string
		mime     string
		want     int
	}{
		{"pdf", "doc.pdf", "application/pdf", paddleFileTypePDF},
		{"image", "scan.png", "image/png", paddleFileTypeImage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := `{"errorCode":0,"result":{"layoutParsingResults":[{"markdown":{"text":"x"}}]}}`
			server, capture := newPaddleServer(t, http.StatusOK, body)
			provider := NewPaddleOCRProvider(server.URL, "", 5*time.Second, nil)

			path := writeTempFile(t, tc.fileName, []byte("raw bytes"))
			if _, err := provider.ExtractText(context.Background(), path, tc.mime); err != nil {
				t.Fatalf("ExtractText: %v", err)
			}
			if capture.fileType != tc.want {
				t.Errorf("fileType = %d, want %d", capture.fileType, tc.want)
			}
			// Bare base64, with no data: prefix -- PaddleX rejects the data URL
			// form that Mistral requires, which is the easy mistake here.
			if strings.HasPrefix(capture.file, "data:") {
				t.Errorf("file was sent as a data URL: %q", capture.file)
			}
			decoded, err := base64.StdEncoding.DecodeString(capture.file)
			if err != nil {
				t.Fatalf("file is not base64: %v", err)
			}
			if string(decoded) != "raw bytes" {
				t.Errorf("decoded file = %q", decoded)
			}
		})
	}
}

func TestPaddleOCRPipelineSelectsTheEndpoint(t *testing.T) {
	tests := []struct {
		pipeline string
		want     string
	}{
		{"", "/layout-parsing"},
		{"pp-structurev3", "/layout-parsing"},
		{"PP-StructureV3", "/layout-parsing"},
		{"ocr", "/ocr"},
		{"table-recognition", "/table-recognition"},
	}
	for _, tc := range tests {
		t.Run(tc.pipeline, func(t *testing.T) {
			body := `{"errorCode":0,"result":{"ocrResults":[{"prunedResult":{"rec_texts":["x"]}}]}}`
			server, capture := newPaddleServer(t, http.StatusOK, body)
			provider := NewPaddleOCRProvider(server.URL, tc.pipeline, 5*time.Second, nil)

			path := writeTempFile(t, "doc.pdf", []byte("%PDF"))
			if _, err := provider.ExtractText(context.Background(), path, "application/pdf"); err != nil {
				t.Fatalf("ExtractText: %v", err)
			}
			if capture.path != tc.want {
				t.Errorf("path = %q, want %q", capture.path, tc.want)
			}
		})
	}
}

func TestPaddleOCRErrors(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    string
		wantSub string
	}{
		{
			// PaddleX reports some failures in the body with a 200 status, so a
			// parser that only checked the status line would return "".
			name:    "non-zero errorCode with HTTP 200",
			status:  http.StatusOK,
			body:    `{"errorCode":1,"errorMsg":"unsupported file type"}`,
			wantSub: "unsupported file type",
		},
		{
			name:    "http error carries errorMsg",
			status:  http.StatusBadRequest,
			body:    `{"errorCode":2,"errorMsg":"bad base64"}`,
			wantSub: "bad base64",
		},
		{
			name:    "no text anywhere",
			status:  http.StatusOK,
			body:    `{"errorCode":0,"result":{"layoutParsingResults":[{}]}}`,
			wantSub: "empty text",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newPaddleServer(t, tc.status, tc.body)
			provider := NewPaddleOCRProvider(server.URL, "", 5*time.Second, nil)

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

func TestPaddleOCRRefusesOfficeDocumentsAndSaysWhatDoes(t *testing.T) {
	server, capture := newPaddleServer(t, http.StatusOK, `{"errorCode":0}`)
	provider := NewPaddleOCRProvider(server.URL, "", 5*time.Second, nil)

	path := writeTempFile(t, "deck.pptx", []byte("PK"))
	_, err := provider.ExtractText(context.Background(), path, "")
	if err == nil {
		t.Fatal("want an error")
	}
	// PaddleX reads pixels, not OOXML. Naming the alternative is the difference
	// between a dead end and a next step.
	if !strings.Contains(err.Error(), "docling") {
		t.Errorf("error = %v, want it to name docling", err)
	}
	if calls := capture.calls.Load(); calls != 0 {
		t.Errorf("sidecar called %d times for an unsupported type", calls)
	}
}

func TestPaddleOCRSerializesRequests(t *testing.T) {
	if got := NewPaddleOCRProvider("http://paddleocr:8080", "", 0, nil).MaxConcurrency(); got != 1 {
		t.Errorf("MaxConcurrency = %d, want 1", got)
	}
}
