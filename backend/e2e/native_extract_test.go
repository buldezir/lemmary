package e2e

import (
	"strings"
	"testing"
)

func TestNativeTextExtractionFormats(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	cases := []struct {
		file   string
		needle string
	}{
		{"sample.txt", "plain text"},
		{"sample.csv", "Paper"},
		{"sample.docx", "INV-9002"},
		{"sample.xlsx", "INV-9003"},
	}

	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			rec := h.uploadDocument(t, token, h.UserID, fixturePath(tc.file))
			id := jsonGetString(rec, "id")
			doc := h.waitDocumentStatus(t, token, id, "completed", "needs_review")
			ocr := jsonGetString(doc, "ocr_text")
			if !strings.Contains(ocr, tc.needle) {
				t.Fatalf("expected ocr_text to contain %q, got %q", tc.needle, ocr)
			}
		})
	}
}
