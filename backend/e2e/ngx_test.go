package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"testing"
)

func TestNgxTokenAndDocuments(t *testing.T) {
	h := StartShared(t)

	status, raw := h.doJSON(t, http.MethodPost, "/api/token/", "", map[string]string{
		"username": UserEmail,
		"password": UserPassword,
	})
	requireStatus(t, status, http.StatusOK, raw)
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(raw), &tok); err != nil || tok.Token == "" {
		t.Fatalf("token response: %s", raw)
	}
	auth := "Token " + tok.Token

	// Create tag via ngx API.
	status, raw = h.doJSON(t, http.MethodPost, "/api/tags/", auth, map[string]any{
		"name": "e2e-tag",
	})
	if status < 200 || status >= 300 {
		t.Fatalf("create tag: %s", formatErr(status, raw))
	}
	var tag map[string]any
	_ = json.Unmarshal([]byte(raw), &tag)

	status, raw = h.doJSON(t, http.MethodPost, "/api/correspondents/", auth, map[string]any{
		"name": "E2E Correspondent",
	})
	if status < 200 || status >= 300 {
		t.Fatalf("create correspondent: %s", formatErr(status, raw))
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/document_types/", auth, map[string]any{
		"name": "E2E Type",
	})
	if status < 200 || status >= 300 {
		t.Fatalf("create document type: %s", formatErr(status, raw))
	}

	// Upload via post_document (unique bytes to avoid checksum collisions across shared harness).
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("document", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(uniquifyFixture(t, fixturePath("sample.png"))); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	req, err := http.NewRequest(http.MethodPost, h.BaseURL+"/api/documents/post_document/", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Authorization", auth)
	resp, err := h.HTTP.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("post_document: %s", formatErr(resp.StatusCode, string(body)))
	}

	status, raw = h.doJSON(t, http.MethodGet, "/api/documents/", auth, nil)
	requireStatus(t, status, http.StatusOK, raw)
	requireContains(t, raw, "results")

	// List tags/correspondents/types.
	for _, path := range []string{"/api/tags/", "/api/correspondents/", "/api/document_types/"} {
		status, raw = h.doJSON(t, http.MethodGet, path, auth, nil)
		requireStatus(t, status, http.StatusOK, raw)
	}
}

func TestNgxDownload(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	pbID := jsonGetString(rec, "id")

	status, raw := h.doJSON(t, http.MethodPost, "/api/token/", "", map[string]string{
		"username": UserEmail,
		"password": UserPassword,
	})
	requireStatus(t, status, http.StatusOK, raw)
	var tok struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal([]byte(raw), &tok)

	// Find ngx document id from list.
	status, raw = h.doJSON(t, http.MethodGet, "/api/documents/?page_size=100", "Token "+tok.Token, nil)
	requireStatus(t, status, http.StatusOK, raw)

	var list struct {
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var ngxID any
	for _, doc := range list.Results {
		// Match by checking download works for any recent doc; prefer title/original.
		if ngxID == nil {
			ngxID = doc["id"]
		}
		// Prefer the one we just uploaded if original_file_name matches.
		if name, _ := doc["original_file_name"].(string); name == "sample.txt" {
			ngxID = doc["id"]
			break
		}
		_ = pbID
	}
	if ngxID == nil {
		t.Fatalf("no documents in ngx list: %s", raw)
	}

	status, body, _ := h.doRaw(t, http.MethodGet, "/api/documents/"+formatNgxID(ngxID)+"/download/", "Token "+tok.Token, nil, "")
	requireStatus(t, status, http.StatusOK, body)
	requireContains(t, body, "Acme Plumbing")
}

func TestNgxDocumentsQuery(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	pbID := jsonGetString(rec, "id")
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+pbID, token, nil)
	})
	_ = h.waitDocumentStatus(t, token, pbID, "completed", "needs_review")

	status, raw := h.doJSON(t, http.MethodPost, "/api/token/", "", map[string]string{
		"username": UserEmail,
		"password": UserPassword,
	})
	requireStatus(t, status, http.StatusOK, raw)
	var tok struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(raw), &tok); err != nil || tok.Token == "" {
		t.Fatalf("token response: %s", raw)
	}

	status, raw = h.doJSON(t, http.MethodGet, "/api/documents/?query="+url.QueryEscape("Acme Plumbing")+"&page_size=100", "Token "+tok.Token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var list struct {
		Count   int              `json:"count"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		t.Fatalf("decode list: %v body %s", err, raw)
	}
	if list.Count == 0 || len(list.Results) == 0 {
		t.Fatalf("expected ngx query hits: %s", raw)
	}
}

func formatNgxID(id any) string {
	switch v := id.(type) {
	case float64:
		return jsonNumber(v)
	case json.Number:
		return v.String()
	case string:
		return v
	default:
		b, _ := json.Marshal(v)
		return string(b)
	}
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
