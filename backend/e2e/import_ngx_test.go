package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestImportNgxUnauthorized(t *testing.T) {
	h := StartShared(t)
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/ngx", "", map[string]any{
		"url":     "http://example.com",
		"api_key": "x",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/import/ngx/status?job_id=missing", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("status poll=%d body=%s", status, raw)
	}
}

func TestImportNgxValidation(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/ngx", token, map[string]any{
		"url":     "",
		"api_key": "x",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/import/ngx", token, map[string]any{
		"url":     "http://example.com",
		"api_key": "",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/import/ngx", token, map[string]any{
		"url":     "http://example.com",
		"api_key": "x",
		"mode":    "nope",
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid mode status=%d body=%s", status, raw)
	}
}

func TestImportNgxPreserveMetadata(t *testing.T) {
	h := StartShared(t)

	corrID := 2
	typeID := 3
	payload := []byte("imported-file-body-" + t.Name())
	checksum := sha256Hex(payload)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		assertToken(t, r, "remote-token")
		writeNgxPage(w, []map[string]any{{"id": 1, "name": "ImportedTag"}})
	})
	mux.HandleFunc("/api/correspondents/", func(w http.ResponseWriter, r *http.Request) {
		writeNgxPage(w, []map[string]any{{"id": corrID, "name": "ImportedCorp"}})
	})
	mux.HandleFunc("/api/document_types/", func(w http.ResponseWriter, r *http.Request) {
		writeNgxPage(w, []map[string]any{{"id": typeID, "name": "ImportedType"}})
	})
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/download") {
			w.Header().Set("Content-Disposition", `attachment; filename="note.txt"`)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(payload)
			return
		}
		writeNgxPage(w, []map[string]any{{
			"id":                 42,
			"title":              "RemoteInvoice",
			"content":            "Imported OCR content for AI",
			"tags":               []int{1},
			"correspondent":      corrID,
			"document_type":      typeID,
			"created_date":       "2024-06-15",
			"original_file_name": "note.txt",
		}})
	})
	remote := httptest.NewServer(mux)
	t.Cleanup(remote.Close)

	token := h.userToken(t)
	result := runImportNgx(t, h, token, remote.URL, "remote-token", "preserve")
	if intFromAny(result["imported"]) != 1 {
		t.Fatalf("imported=%v body=%v", result["imported"], result)
	}
	if intFromAny(result["tags_upserted"]) != 1 {
		t.Fatalf("tags_upserted=%v", result["tags_upserted"])
	}
	if intFromAny(result["failed"]) != 0 {
		t.Fatalf("failed=%v errors=%v", result["failed"], result["errors"])
	}

	// Second import of same bytes should skip as duplicate.
	result = runImportNgx(t, h, token, remote.URL, "remote-token", "preserve")
	if intFromAny(result["imported"]) != 0 || intFromAny(result["skipped_duplicates"]) != 1 {
		t.Fatalf("second import result=%v", result)
	}
	if intFromAny(result["tags_upserted"]) != 0 {
		t.Fatalf("second import should not count existing tags as upserted: %v", result["tags_upserted"])
	}

	docsStatus, docsRaw := h.doJSON(t, http.MethodGet,
		`/api/collections/documents/records?filter=checksum="`+checksum+`"&perPage=50`, token, nil)
	requireStatus(t, docsStatus, http.StatusOK, docsRaw)
	var docs struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(docsRaw), &docs); err != nil {
		t.Fatalf("decode docs: %v", err)
	}
	if len(docs.Items) != 1 {
		t.Fatalf("expected 1 document, got %d: %s", len(docs.Items), docsRaw)
	}
	doc := docs.Items[0]
	ocr, _ := doc["ocr_text"].(string)
	if ocr != "Imported OCR content for AI" {
		t.Fatalf("ocr_text=%q", ocr)
	}
	if title, _ := doc["title"].(string); title != "RemoteInvoice" {
		t.Fatalf("title=%q", title)
	}
	if date, _ := doc["document_date"].(string); !strings.HasPrefix(date, "2024-06-15") {
		t.Fatalf("document_date=%q", date)
	}
	if corr, _ := doc["correspondent"].(string); corr == "" {
		t.Fatal("expected correspondent to be set")
	}
	if dtype, _ := doc["document_type"].(string); dtype == "" {
		t.Fatal("expected document_type to be set")
	}
	if user, _ := doc["user"].(string); user != h.UserID {
		t.Fatalf("imported document owner=%q want user %q", user, h.UserID)
	}
}

func TestImportNgxReprocessFilesOnly(t *testing.T) {
	h := StartShared(t)

	payload := []byte("imported-reprocess-body-" + t.Name())
	checksum := sha256Hex(payload)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		writeNgxPage(w, []map[string]any{{"id": 1, "name": "ShouldNotImport"}})
	})
	mux.HandleFunc("/api/correspondents/", func(w http.ResponseWriter, r *http.Request) {
		writeNgxPage(w, []map[string]any{{"id": 2, "name": "ShouldNotImportCorp"}})
	})
	mux.HandleFunc("/api/document_types/", func(w http.ResponseWriter, r *http.Request) {
		writeNgxPage(w, []map[string]any{{"id": 3, "name": "ShouldNotImportType"}})
	})
	mux.HandleFunc("/api/documents/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/download") {
			w.Header().Set("Content-Disposition", `attachment; filename="reprocess.txt"`)
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write(payload)
			return
		}
		corr := 2
		dtype := 3
		writeNgxPage(w, []map[string]any{{
			"id":                 99,
			"title":              "ShouldNotKeepTitle",
			"content":            "ShouldNotKeepOCR",
			"tags":               []int{1},
			"correspondent":      corr,
			"document_type":      dtype,
			"created_date":       "2023-01-01",
			"original_file_name": "reprocess.txt",
		}})
	})
	remote := httptest.NewServer(mux)
	t.Cleanup(remote.Close)

	token := h.userToken(t)
	result := runImportNgx(t, h, token, remote.URL, "remote-token", "reprocess")
	if intFromAny(result["imported"]) != 1 {
		t.Fatalf("imported=%v body=%v", result["imported"], result)
	}
	if intFromAny(result["tags_upserted"]) != 0 ||
		intFromAny(result["correspondents_upserted"]) != 0 ||
		intFromAny(result["document_types_upserted"]) != 0 {
		t.Fatalf("reprocess should not upsert taxonomy: %v", result)
	}

	docsStatus, docsRaw := h.doJSON(t, http.MethodGet,
		`/api/collections/documents/records?filter=checksum="`+checksum+`"&perPage=50`, token, nil)
	requireStatus(t, docsStatus, http.StatusOK, docsRaw)
	var docs struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal([]byte(docsRaw), &docs); err != nil {
		t.Fatalf("decode docs: %v", err)
	}
	if len(docs.Items) < 1 {
		t.Fatalf("expected imported document, got %s", docsRaw)
	}
	doc := docs.Items[0]
	if title, _ := doc["title"].(string); title == "ShouldNotKeepTitle" {
		t.Fatalf("reprocess mode should not keep ngx title, got %q", title)
	}
	if ocr, _ := doc["ocr_text"].(string); ocr == "ShouldNotKeepOCR" {
		t.Fatalf("reprocess mode should not keep ngx OCR text, got %q", ocr)
	}
	if user, _ := doc["user"].(string); user != h.UserID {
		t.Fatalf("imported document owner=%q want user %q", user, h.UserID)
	}
}

func TestImportNgxJobHiddenFromOtherUser(t *testing.T) {
	h := StartShared(t)
	mux := http.NewServeMux()
	empty := func(w http.ResponseWriter, _ *http.Request) {
		writeNgxPage(w, []map[string]any{})
	}
	mux.HandleFunc("/api/tags/", empty)
	mux.HandleFunc("/api/correspondents/", empty)
	mux.HandleFunc("/api/document_types/", empty)
	mux.HandleFunc("/api/documents/", empty)
	remote := httptest.NewServer(mux)
	t.Cleanup(remote.Close)

	tokenA := h.userToken(t)
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/ngx", tokenA, map[string]any{
		"url":     remote.URL,
		"api_key": "remote-token",
		"mode":    "preserve",
	})
	requireStatus(t, status, http.StatusAccepted, raw)
	var start map[string]any
	if err := json.Unmarshal([]byte(raw), &start); err != nil {
		t.Fatalf("decode start: %v body %s", err, raw)
	}
	jobID, _ := start["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id in %s", raw)
	}

	otherEmail := fmt.Sprintf("import-other-%d@paperless.local", time.Now().UnixNano())
	otherPass := "otherpassword123"
	status, raw = h.doJSON(t, http.MethodPost, "/api/collections/users/records", h.superToken(t), map[string]any{
		"email":           otherEmail,
		"password":        otherPass,
		"passwordConfirm": otherPass,
		"verified":        true,
	})
	if status < 200 || status >= 300 {
		if _, err := createAuthRecord(h.App, "users", otherEmail, otherPass); err != nil {
			t.Fatalf("create other user via API (%s) and app (%v)", formatErr(status, raw), err)
		}
	}
	tokenB := h.authWithPassword(t, "users", otherEmail, otherPass).Token
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/import/ngx/status?job_id="+jobID, tokenB, nil)
	if status != http.StatusNotFound {
		t.Fatalf("other user should not see import job: status=%d body=%s", status, raw)
	}

	_ = waitImportNgx(t, h, tokenA, jobID)
}

func runImportNgx(t *testing.T, h *Harness, token, url, apiKey, mode string) map[string]any {
	t.Helper()
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/ngx", token, map[string]any{
		"url":     url,
		"api_key": apiKey,
		"mode":    mode,
	})
	requireStatus(t, status, http.StatusAccepted, raw)

	var start map[string]any
	if err := json.Unmarshal([]byte(raw), &start); err != nil {
		t.Fatalf("decode start: %v body %s", err, raw)
	}
	jobID, _ := start["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id in %s", raw)
	}
	return waitImportNgx(t, h, token, jobID)
}

func waitImportNgx(t *testing.T, h *Harness, token, jobID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		status, raw := h.doJSON(t, http.MethodGet, "/api/app/import/ngx/status?job_id="+jobID, token, nil)
		requireStatus(t, status, http.StatusOK, raw)
		var payload map[string]any
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			t.Fatalf("decode status: %v body %s", err, raw)
		}
		switch payload["status"] {
		case "completed":
			result, _ := payload["result"].(map[string]any)
			if result == nil {
				t.Fatalf("completed without result: %s", raw)
			}
			return result
		case "failed":
			t.Fatalf("import failed: %s", raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("import timed out for job %s", jobID)
	return nil
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func assertToken(t *testing.T, r *http.Request, want string) {
	t.Helper()
	got := r.Header.Get("Authorization")
	if got != "Token "+want {
		t.Fatalf("Authorization=%q", got)
	}
}

func writeNgxPage(w http.ResponseWriter, results []map[string]any) {
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count":    len(results),
		"next":     nil,
		"previous": nil,
		"results":  results,
	})
}

func intFromAny(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return -1
	}
}
