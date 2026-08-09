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
}

func TestImportNgxFromRemote(t *testing.T) {
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
	})
	requireStatus(t, status, http.StatusOK, raw)
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if intFromAny(result["imported"]) != 0 || intFromAny(result["skipped_duplicates"]) != 1 {
		t.Fatalf("second import result=%s", raw)
	}

	docsStatus, docsRaw := h.doJSON(t, http.MethodGet,
		`/api/collections/documents/records?filter=title~"RemoteInvoice"&perPage=50`, token, nil)
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
	ocr, _ := docs.Items[0]["ocr_text"].(string)
	if ocr != "Imported OCR content for AI" {
		t.Fatalf("ocr_text=%q", ocr)
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
