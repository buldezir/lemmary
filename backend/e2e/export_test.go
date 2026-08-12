package e2e

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDocumentsExportArchive(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	id := jsonGetString(rec, "id")
	h.settleDocuments(t, id)

	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/tags/records", token, map[string]any{
		"name": "export-e2e-tag-" + id,
	})
	requireStatus(t, status, http.StatusOK, raw)
	var tag map[string]any
	if err := json.Unmarshal([]byte(raw), &tag); err != nil {
		t.Fatalf("decode tag: %v", err)
	}
	tagID := jsonGetString(tag, "id")
	tagName := jsonGetString(tag, "name")

	const wantOCR = "OCR text for export e2e"
	const wantTitle = "Export Invoice"
	status, raw = h.doJSON(t, http.MethodPatch, "/api/collections/documents/records/"+id, token, map[string]any{
		"title":                   wantTitle,
		"ocr_text":                wantOCR,
		"tags":                    []string{tagID},
		"people_or_organizations": []string{"Acme Plumbing"},
		"purpose":                 "Plumbing invoice",
		"processing_status":       "completed",
	})
	requireStatus(t, status, http.StatusOK, raw)
	doc := mustDecodeMap(t, raw)
	if jsonGetString(doc, "title") != wantTitle || jsonGetString(doc, "ocr_text") != wantOCR {
		t.Fatalf("patch did not stick: title=%q ocr=%q", jsonGetString(doc, "title"), jsonGetString(doc, "ocr_text"))
	}
	fileName := jsonGetString(doc, "file")
	if fileName == "" {
		t.Fatal("missing file name")
	}
	stem := strings.TrimSuffix(fileName, filepath.Ext(fileName))

	t.Run("unauthenticated", func(t *testing.T) {
		status, body, _ := h.doRaw(t, http.MethodGet, "/api/app/documents/export?mode=originals", "", nil, "")
		requireStatus(t, status, http.StatusUnauthorized, body)
	})

	t.Run("invalid_mode", func(t *testing.T) {
		status, body, _ := h.doRaw(t, http.MethodGet, "/api/app/documents/export?mode=nope", token, nil, "")
		requireStatus(t, status, http.StatusBadRequest, body)
	})

	t.Run("originals", func(t *testing.T) {
		entries := fetchExportZip(t, h, token, "originals")
		assertZipHas(t, entries, id+"/"+fileName)
		assertZipMissing(t, entries, id+"/"+stem+".ocr.txt")
		assertZipMissing(t, entries, id+"/"+stem+".metadata.json")
		if !strings.Contains(entries[id+"/"+fileName], "Acme Plumbing") {
			t.Fatalf("original content missing fixture text")
		}
	})

	t.Run("ocr", func(t *testing.T) {
		entries := fetchExportZip(t, h, token, "ocr")
		assertZipHas(t, entries, id+"/"+fileName)
		assertZipHas(t, entries, id+"/"+stem+".ocr.txt")
		if entries[id+"/"+stem+".ocr.txt"] != wantOCR {
			t.Fatalf("ocr sidecar=%q", entries[id+"/"+stem+".ocr.txt"])
		}
		assertZipMissing(t, entries, id+"/"+stem+".metadata.json")
	})

	t.Run("metadata", func(t *testing.T) {
		entries := fetchExportZip(t, h, token, "metadata")
		assertZipHas(t, entries, id+"/"+fileName)
		assertZipHas(t, entries, id+"/"+stem+".ocr.txt")
		metaPath := id + "/" + stem + ".metadata.json"
		assertZipHas(t, entries, metaPath)
		var meta map[string]any
		if err := json.Unmarshal([]byte(entries[metaPath]), &meta); err != nil {
			t.Fatalf("decode metadata: %v", err)
		}
		if meta["title"] != wantTitle {
			t.Fatalf("title=%v", meta["title"])
		}
		if meta["ocr_text"] != wantOCR {
			t.Fatalf("ocr_text=%v", meta["ocr_text"])
		}
		if meta["original_filename"] != fileName {
			t.Fatalf("original_filename=%v", meta["original_filename"])
		}
		tags, _ := meta["tags"].([]any)
		foundTag := false
		for _, item := range tags {
			if item == tagName {
				foundTag = true
				break
			}
		}
		if !foundTag {
			t.Fatalf("expected tag name %q in metadata, got %#v", tagName, meta["tags"])
		}
	})

	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+id, token, nil)
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/tags/records/"+tagID, token, nil)
	})
}

func TestDocumentsExportOwnerIsolation(t *testing.T) {
	h := StartShared(t)
	tokenA := h.userToken(t)
	rec := h.uploadDocument(t, tokenA, h.UserID, fixturePath("sample.txt"))
	idA := jsonGetString(rec, "id")
	h.settleDocuments(t, idA)
	fileA := jsonGetString(h.getDocument(t, tokenA, idA), "file")

	otherEmail := "export-other-e2e@paperless.local"
	otherPass := "otherpassword123"
	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/users/records", h.superToken(t), map[string]any{
		"email":           otherEmail,
		"password":        otherPass,
		"passwordConfirm": otherPass,
		"verified":        true,
	})
	otherID := ""
	if status >= 200 && status < 300 {
		otherID = jsonGetString(mustDecodeMap(t, raw), "id")
	} else {
		var err error
		otherID, err = createAuthRecord(h.App, "users", otherEmail, otherPass)
		if err != nil {
			t.Fatalf("create other user via API (%s) and app (%v)", formatErr(status, raw), err)
		}
	}

	authB := h.authWithPassword(t, "users", otherEmail, otherPass)
	tokenB := authB.Token
	if otherID == "" {
		otherID = jsonGetString(authB.Record, "id")
	}

	recB := h.uploadDocument(t, tokenB, otherID, fixturePath("sample.txt"))
	idB := jsonGetString(recB, "id")
	h.settleDocuments(t, idA, idB)
	fileB := jsonGetString(h.getDocument(t, tokenB, idB), "file")

	entriesA := fetchExportZip(t, h, tokenA, "originals")
	assertZipHas(t, entriesA, idA+"/"+fileA)
	assertZipMissing(t, entriesA, idB+"/"+fileB)

	entriesB := fetchExportZip(t, h, tokenB, "originals")
	assertZipHas(t, entriesB, idB+"/"+fileB)
	assertZipMissing(t, entriesB, idA+"/"+fileA)

	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+idA, tokenA, nil)
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+idB, tokenB, nil)
	})
}

func fetchExportZip(t *testing.T, h *Harness, token, mode string) map[string]string {
	t.Helper()
	status, body, headers := h.doRaw(t, http.MethodGet, "/api/app/documents/export?mode="+mode, token, nil, "")
	requireStatus(t, status, http.StatusOK, body)
	if ct := headers.Get("Content-Type"); !strings.Contains(ct, "application/zip") {
		t.Fatalf("Content-Type=%q", ct)
	}
	if !strings.Contains(headers.Get("Content-Disposition"), "paperless-export.zip") {
		t.Fatalf("Content-Disposition=%q", headers.Get("Content-Disposition"))
	}

	data := []byte(body)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		raw, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(raw)
	}
	return out
}

func assertZipHas(t *testing.T, entries map[string]string, name string) {
	t.Helper()
	if _, ok := entries[name]; !ok {
		keys := make([]string, 0, len(entries))
		for k := range entries {
			keys = append(keys, k)
		}
		t.Fatalf("missing zip entry %q; have %v", name, keys)
	}
}

func assertZipMissing(t *testing.T, entries map[string]string, name string) {
	t.Helper()
	if _, ok := entries[name]; ok {
		t.Fatalf("unexpected zip entry %q", name)
	}
}

func mustDecodeMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decode map: %v body %s", err, raw)
	}
	return m
}
