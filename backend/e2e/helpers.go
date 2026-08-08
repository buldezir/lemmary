package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type authResult struct {
	Token  string
	Record map[string]any
}

func (h *Harness) authWithPassword(t testing.TB, collection, identity, password string) authResult {
	t.Helper()
	body := map[string]string{
		"identity": identity,
		"password": password,
	}
	var out struct {
		Token  string         `json:"token"`
		Record map[string]any `json:"record"`
	}
	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/"+collection+"/auth-with-password", "", body)
	if status != http.StatusOK {
		t.Fatalf("auth %s as %s: status %d body %s", collection, identity, status, raw)
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode auth: %v body %s", err, raw)
	}
	if out.Token == "" {
		t.Fatal("empty auth token")
	}
	return authResult{Token: out.Token, Record: out.Record}
}

func (h *Harness) userToken(t testing.TB) string {
	t.Helper()
	return h.authWithPassword(t, "users", UserEmail, UserPassword).Token
}

func (h *Harness) superToken(t testing.TB) string {
	t.Helper()
	return h.authWithPassword(t, "_superusers", SuperEmail, SuperPassword).Token
}

func (h *Harness) doJSON(t testing.TB, method, path, token string, body any) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, h.BaseURL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (h *Harness) doRaw(t testing.TB, method, path, token string, body io.Reader, contentType string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(method, h.BaseURL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		req.Header.Set("Authorization", token)
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), resp.Header.Clone()
}

func (h *Harness) uploadDocument(t testing.TB, token, userID, filePath string) map[string]any {
	t.Helper()
	return h.uploadDocumentBytes(t, token, userID, uniquifyFixture(t, filePath), filepath.Base(filePath))
}

// uploadDocumentExact uploads the fixture bytes unchanged (for duplicate checksum tests).
func (h *Harness) uploadDocumentExact(t testing.TB, token, userID, filePath string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return h.uploadDocumentBytes(t, token, userID, data, filepath.Base(filePath))
}

func (h *Harness) uploadDocumentBytes(t testing.TB, token, userID string, data []byte, fileName string) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("user", userID)
	_ = w.WriteField("processing_status", "pending")
	part, err := w.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	status, raw, _ := h.doRaw(t, http.MethodPost, "/api/collections/documents/records", token, &buf, w.FormDataContentType())
	if status < 200 || status >= 300 {
		t.Fatalf("upload document: status %d body %s", status, raw)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("decode document: %v body %s", err, raw)
	}
	return rec
}

func (h *Harness) tryUploadDocumentExact(t testing.TB, token, userID, filePath string) (int, string) {
	t.Helper()
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("user", userID)
	_ = w.WriteField("processing_status", "pending")
	part, err := w.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write file: %v", err)
	}
	_ = w.Close()
	status, raw, _ := h.doRaw(t, http.MethodPost, "/api/collections/documents/records", token, &buf, w.FormDataContentType())
	return status, raw
}

// uniquifyFixture returns fixture bytes with a unique trailer so checksums differ across uploads.
// Office fixtures are left unchanged (native parsers require a valid container).
func uniquifyFixture(t testing.TB, filePath string) []byte {
	t.Helper()
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	switch ext {
	case ".docx", ".xlsx":
		return data
	default:
		trailer := fmt.Sprintf("\n#e2e-%d-%s\n", time.Now().UnixNano(), t.Name())
		return append(append([]byte{}, data...), []byte(trailer)...)
	}
}

func (h *Harness) getDocument(t testing.TB, token, id string) map[string]any {
	t.Helper()
	status, raw := h.doJSON(t, http.MethodGet, "/api/collections/documents/records/"+id, token, nil)
	if status != http.StatusOK {
		t.Fatalf("get document: status %d body %s", status, raw)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return rec
}

// stopDocumentJobs marks create-triggered processing jobs failed so the worker
// cannot race with duplicate-scan assertions in the shared harness.
func (h *Harness) stopDocumentJobs(t testing.TB, documentIDs ...string) {
	t.Helper()
	for _, docID := range documentIDs {
		jobs, err := h.App.FindRecordsByFilter(
			"processing_jobs",
			"document = {:docId}",
			"",
			50,
			0,
			map[string]any{"docId": docID},
		)
		if err != nil {
			t.Fatalf("list jobs for %s: %v", docID, err)
		}
		for _, job := range jobs {
			status := job.GetString("status")
			if status == "completed" || status == "failed" || status == "needs_review" {
				continue
			}
			job.Set("status", "failed")
			job.Set("last_error", "stopped by e2e harness")
			if err := h.App.Save(job); err != nil {
				t.Fatalf("stop job %s: %v", job.Id, err)
			}
		}
	}
}

// settleDocuments stops jobs and waits briefly so in-flight worker saves finish
// before tests mutate documents for duplicate-scan assertions.
func (h *Harness) settleDocuments(t testing.TB, documentIDs ...string) {
	t.Helper()
	h.stopDocumentJobs(t, documentIDs...)
	time.Sleep(300 * time.Millisecond)
	h.stopDocumentJobs(t, documentIDs...)
	for _, docID := range documentIDs {
		doc, err := h.App.FindRecordById("documents", docID)
		if err != nil {
			t.Fatalf("load document %s: %v", docID, err)
		}
		if doc.GetString("processing_status") == "processing" || doc.GetString("processing_status") == "pending" {
			doc.Set("processing_status", "completed")
			if err := h.App.Save(doc); err != nil {
				t.Fatalf("settle document %s: %v", docID, err)
			}
		}
	}
}

func (h *Harness) waitDocumentStatus(t testing.TB, token, id string, want ...string) map[string]any {
	t.Helper()
	wantSet := map[string]struct{}{}
	for _, s := range want {
		wantSet[s] = struct{}{}
	}
	deadline := time.Now().Add(60 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		last = h.getDocument(t, token, id)
		status, _ := last["processing_status"].(string)
		if _, ok := wantSet[status]; ok {
			return last
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("document %s did not reach %v; last=%v", id, want, last)
	return nil
}

func fixturePath(name string) string {
	return filepath.Join("testdata", name)
}

func mustDecodeList(t testing.TB, raw string) []map[string]any {
	t.Helper()
	var out struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode list: %v body %s", err, raw)
	}
	return out.Items
}

func jsonGetString(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func requireContains(t testing.TB, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("expected %q in %q", needle, haystack)
	}
}

func requireStatus(t testing.TB, got, want int, body string) {
	t.Helper()
	if got != want {
		t.Fatalf("status %d want %d body %s", got, want, body)
	}
}

func formatErr(status int, body string) string {
	return fmt.Sprintf("status=%d body=%s", status, body)
}
