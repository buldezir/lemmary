package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"testing"
)

func TestAppOCRProviders(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	status, raw := h.doJSON(t, http.MethodGet, "/api/app/ocr/providers", token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	requireContains(t, raw, "mistral")
}

func TestAppOCRTest(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	status, raw := h.doJSON(t, http.MethodGet, "/api/app/ocr/providers", token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	var body struct {
		Providers []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			SDK  string `json:"sdk"`
		} `json:"providers"`
	}
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("decode providers: %v", err)
	}
	providerID := ""
	for _, p := range body.Providers {
		if p.SDK == "mistral" {
			providerID = p.ID
			break
		}
	}
	if providerID == "" {
		t.Fatalf("no mistral provider in %s", raw)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("provider", providerID)
	_ = w.WriteField("model", "mistral-ocr-latest")
	part, err := w.CreateFormFile("file", "sample.png")
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(fixturePath("sample.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := io.Copy(part, f); err != nil {
		t.Fatal(err)
	}
	_ = w.Close()

	status, raw, _ = h.doRaw(t, http.MethodPost, "/api/app/ocr/test", token, &buf, w.FormDataContentType())
	requireStatus(t, status, http.StatusOK, raw)
	requireContains(t, raw, "Acme Plumbing")
}
