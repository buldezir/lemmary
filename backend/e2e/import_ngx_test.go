package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestImportNgxForbiddenForUser(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/ngx", token, map[string]any{
		"url":     "http://example.com",
		"api_key": "x",
	})
	if status != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", status, raw)
	}
}

func TestImportNgxValidation(t *testing.T) {
	h := StartShared(t)
	token := h.adminUserToken(t)
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

	token := h.adminUserToken(t)
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/ngx", token, map[string]any{
		"url":     remote.URL,
		"api_key": "remote-token",
		"mode":    "preserve",
	})
	requireStatus(t, status, http.StatusOK, raw)

	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode: %v body %s", err, raw)
	}
	if intFromAny(result["imported"]) != 1 {
		t.Fatalf("imported=%v body=%s", result["imported"], raw)
	}
	if intFromAny(result["tags_upserted"]) != 1 {
		t.Fatalf("tags_upserted=%v", result["tags_upserted"])
	}
	if intFromAny(result["failed"]) != 0 {
		t.Fatalf("failed=%v errors=%v", result["failed"], result["errors"])
	}

	// Second import of same bytes should skip as duplicate.
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/import/ngx", token, map[string]any{
		"url":     remote.URL,
		"api_key": "remote-token",
		"mode":    "preserve",
	})
	requireStatus(t, status, http.StatusOK, raw)
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if intFromAny(result["imported"]) != 0 || intFromAny(result["skipped_duplicates"]) != 1 {
		t.Fatalf("second import result=%s", raw)
	}

	docsStatus, docsRaw := h.doJSON(t, http.MethodGet,
		`/api/collections/documents/records?filter=title="RemoteInvoice"&perPage=50`, token, nil)
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
}

func TestImportNgxReprocessFilesOnly(t *testing.T) {
	h := StartShared(t)

	payload := []byte("imported-reprocess-body-" + t.Name())
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

	token := h.adminUserToken(t)
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/ngx", token, map[string]any{
		"url":     remote.URL,
		"api_key": "remote-token",
		"mode":    "reprocess",
	})
	requireStatus(t, status, http.StatusOK, raw)

	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode: %v body %s", err, raw)
	}
	if intFromAny(result["imported"]) != 1 {
		t.Fatalf("imported=%v body=%s", result["imported"], raw)
	}
	if intFromAny(result["tags_upserted"]) != 0 ||
		intFromAny(result["correspondents_upserted"]) != 0 ||
		intFromAny(result["document_types_upserted"]) != 0 {
		t.Fatalf("reprocess should not upsert taxonomy: %s", raw)
	}

	docsStatus, docsRaw := h.doJSON(t, http.MethodGet,
		`/api/collections/documents/records?filter=file~"reprocess"&perPage=50&sort=-created`, token, nil)
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
