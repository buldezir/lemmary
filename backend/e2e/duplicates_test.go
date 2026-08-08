package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDuplicateChecksumRejected(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	first := h.uploadDocumentExact(t, token, h.UserID, fixturePath("sample.txt"))
	firstID := jsonGetString(first, "id")
	if firstID == "" {
		t.Fatal("missing first document id")
	}
	if checksum := jsonGetString(first, "checksum"); checksum == "" {
		t.Fatal("expected checksum on created document")
	}

	status, raw := h.tryUploadDocumentExact(t, token, h.UserID, fixturePath("sample.txt"))
	if status >= 200 && status < 300 {
		t.Fatalf("expected duplicate upload to fail, got %s", formatErr(status, raw))
	}
	if !strings.Contains(strings.ToLower(raw), "duplicate") {
		t.Fatalf("expected duplicate error, got %s", formatErr(status, raw))
	}
	if !strings.Contains(raw, firstID) {
		t.Fatalf("expected existing id %s in error, got %s", firstID, raw)
	}
}

func TestDuplicatesScanBackfillsChecksum(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	super := h.superToken(t)

	doc := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	docID := jsonGetString(doc, "id")
	if jsonGetString(doc, "checksum") == "" {
		t.Fatal("expected checksum on upload")
	}
	h.settleDocuments(t, docID)

	status, raw := h.doJSON(t, http.MethodPatch, "/api/collections/documents/records/"+docID, token, map[string]any{
		"checksum": "",
	})
	requireStatus(t, status, http.StatusOK, raw)
	if jsonGetString(h.getDocument(t, token, docID), "checksum") != "" {
		t.Fatal("expected checksum cleared before scan")
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/duplicates/scan", super, nil)
	requireStatus(t, status, http.StatusOK, raw)

	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode scan: %v", err)
	}
	if int(result["checksum_backfilled"].(float64)) < 1 {
		t.Fatalf("expected checksum_backfilled >= 1, got %v", result)
	}
	if jsonGetString(h.getDocument(t, token, docID), "checksum") == "" {
		t.Fatal("expected checksum restored by scan")
	}
}

func TestDuplicatesScanMarksNear(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	super := h.superToken(t)

	enabled := true
	threshold := 0.85
	status, raw := h.doJSON(t, http.MethodPatch, "/api/app/settings", super, map[string]any{
		"near_duplicate_detection_enabled": enabled,
		"near_duplicate_threshold":         threshold,
	})
	requireStatus(t, status, http.StatusOK, raw)
	t.Cleanup(func() {
		off := false
		_, _ = h.doJSON(t, http.MethodPatch, "/api/app/settings", super, map[string]any{
			"near_duplicate_detection_enabled": off,
		})
	})

	// Unique OCR so this pair does not collide with other fixtures in the shared harness.
	ocr := strings.Repeat("zebra quartz nymph near-dup e2e marker ", 8) +
		fmt.Sprintf("unique-token-%d", time.Now().UnixNano())

	a := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	b := h.uploadDocument(t, token, h.UserID, fixturePath("sample.txt"))
	aID := jsonGetString(a, "id")
	bID := jsonGetString(b, "id")

	h.settleDocuments(t, aID, bID)

	for _, id := range []string{aID, bID} {
		status, raw = h.doJSON(t, http.MethodPatch, "/api/collections/documents/records/"+id, token, map[string]any{
			"ocr_text":          ocr,
			"text_fingerprint":  "",
			"duplicate_of":      "",
			"processing_status": "completed",
		})
		requireStatus(t, status, http.StatusOK, raw)
	}

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/duplicates/scan", super, nil)
	requireStatus(t, status, http.StatusOK, raw)

	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode scan: %v", err)
	}
	if int(result["near_marked"].(float64)) < 1 {
		t.Fatalf("expected near_marked >= 1, got %v", result)
	}

	docA := h.getDocument(t, token, aID)
	docB := h.getDocument(t, token, bID)
	if jsonGetString(docB, "duplicate_of") != aID {
		t.Fatalf("doc B duplicate_of=%q want %q (A=%v B=%v result=%v)",
			jsonGetString(docB, "duplicate_of"), aID, docA, docB, result)
	}
	if jsonGetString(docB, "processing_status") != "needs_review" {
		t.Fatalf("expected needs_review, got %q", jsonGetString(docB, "processing_status"))
	}
	if jsonGetString(docA, "duplicate_of") != "" {
		t.Fatalf("older document must remain the original, got duplicate_of=%q", jsonGetString(docA, "duplicate_of"))
	}
}

func TestDuplicatesScanSuperuserOnly(t *testing.T) {
	h := StartShared(t)
	userTok := h.userToken(t)
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/duplicates/scan", userTok, nil)
	if status == http.StatusOK {
		t.Fatalf("regular user should not scan duplicates: %s", raw)
	}
}
