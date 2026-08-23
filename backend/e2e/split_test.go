package e2e

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"paperless-go/backend/internal/pdftool/testpdf"
)

// splitPDF builds a multi-page PDF whose per-page text is unique to this test
// run, so re-runs against the shared harness do not collide on the checksum of
// an earlier run's parts.
func splitPDF(t *testing.T, pageCount int) []byte {
	t.Helper()
	return testpdf.Multipage(pageCount, "Split fixture "+t.Name(), "Acme Plumbing GmbH")
}

func (h *Harness) uploadSplitPDF(t *testing.T, token string, pdf []byte, fileName string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(pdf); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	status, raw, _ := h.doRaw(t, http.MethodPost, "/api/app/split/upload", token, &body, w.FormDataContentType())
	return status, raw
}

// stageSplitUpload uploads a PDF and returns its upload id.
func (h *Harness) stageSplitUpload(t *testing.T, token string, pdf []byte, fileName string) string {
	t.Helper()
	status, raw := h.uploadSplitPDF(t, token, pdf, fileName)
	requireStatus(t, status, http.StatusOK, raw)
	uploadID, _ := decodeJSONMap(t, raw)["upload_id"].(string)
	if uploadID == "" {
		t.Fatalf("missing upload_id in %s", raw)
	}
	return uploadID
}

// pollSplitJob waits for a job at statusPath to finish and returns its result.
func (h *Harness) pollSplitJob(t *testing.T, token, statusPath, jobID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status, raw := h.doJSON(t, http.MethodGet, statusPath+"?job_id="+jobID, token, nil)
		requireStatus(t, status, http.StatusOK, raw)
		payload := decodeJSONMap(t, raw)
		switch payload["status"] {
		case "completed":
			result, _ := payload["result"].(map[string]any)
			if result == nil {
				t.Fatalf("completed without result: %s", raw)
			}
			return result
		case "failed":
			t.Fatalf("job failed: %s", raw)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("job %s timed out", jobID)
	return nil
}

func (h *Harness) runSplit(t *testing.T, token, uploadID string, parts []map[string]int) map[string]any {
	t.Helper()
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/split", token, map[string]any{
		"upload_id": uploadID,
		"parts":     parts,
	})
	requireStatus(t, status, http.StatusAccepted, raw)
	jobID, _ := decodeJSONMap(t, raw)["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id in %s", raw)
	}
	return h.pollSplitJob(t, token, "/api/app/split/status", jobID)
}

func pageRange(from, to int) map[string]int {
	return map[string]int{"from": from, "to": to}
}

// documentIDsFrom reads the created ids out of a split result.
func documentIDsFrom(t *testing.T, result map[string]any) []string {
	t.Helper()
	raw, _ := result["document_ids"].([]any)
	ids := make([]string, 0, len(raw))
	for _, item := range raw {
		if id, ok := item.(string); ok {
			ids = append(ids, id)
		}
	}
	return ids
}

func TestSplitRequiresAuth(t *testing.T) {
	h := StartShared(t)

	status, raw := h.uploadSplitPDF(t, "", splitPDF(t, 2), "scan.pdf")
	if status != http.StatusUnauthorized {
		t.Fatalf("upload status=%d body=%s", status, raw)
	}
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/app/split", map[string]any{"upload_id": "x"}},
		{http.MethodGet, "/api/app/split/status?job_id=x", nil},
		{http.MethodPost, "/api/app/split/detect", map[string]any{"upload_id": "x"}},
		{http.MethodGet, "/api/app/split/detect/status?job_id=x", nil},
		{http.MethodGet, "/api/app/split/page?upload_id=x&page=1", nil},
		{http.MethodDelete, "/api/app/split/upload?upload_id=x", nil},
	} {
		status, raw := h.doJSON(t, call.method, call.path, "", call.body)
		if status != http.StatusUnauthorized {
			t.Fatalf("%s %s status=%d body=%s", call.method, call.path, status, raw)
		}
	}
}

func TestSplitUploadReportsThePageCount(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	status, raw := h.uploadSplitPDF(t, token, splitPDF(t, 6), "scan 2024.pdf")
	requireStatus(t, status, http.StatusOK, raw)

	payload := decodeJSONMap(t, raw)
	if intFromAny(payload["page_count"]) != 6 {
		t.Fatalf("page_count=%v body=%s", payload["page_count"], raw)
	}
	if payload["file_name"] != "scan 2024.pdf" {
		t.Fatalf("file_name=%v body=%s", payload["file_name"], raw)
	}
	if intFromAny(payload["size_bytes"]) <= 0 {
		t.Fatalf("size_bytes=%v body=%s", payload["size_bytes"], raw)
	}
	uploadID, _ := payload["upload_id"].(string)
	if uploadID == "" {
		t.Fatalf("missing upload_id in %s", raw)
	}

	// Clean up so the staging dir does not keep the fixture around.
	status, raw = h.doJSON(t, http.MethodDelete, "/api/app/split/upload?upload_id="+uploadID, token, nil)
	requireStatus(t, status, http.StatusOK, raw)
}

func TestSplitUploadRejectsUnsplittableFiles(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	cases := []struct {
		name string
		pdf  []byte
		want string
	}{
		{"not a pdf", []byte("this is not a pdf at all"), "not a readable PDF"},
		{"single page", testpdf.Multipage(1), "only one page"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := h.uploadSplitPDF(t, token, tc.pdf, "scan.pdf")
			if status != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", status, raw)
			}
			requireContains(t, raw, tc.want)
		})
	}
}

func TestSplitPageServesThumbnails(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	uploadID := h.stageSplitUpload(t, token, splitPDF(t, 3), "scan.pdf")
	t.Cleanup(func() {
		h.doJSON(t, http.MethodDelete, "/api/app/split/upload?upload_id="+uploadID, token, nil)
	})

	for page := 1; page <= 3; page++ {
		path := fmt.Sprintf("/api/app/split/page?upload_id=%s&page=%d", uploadID, page)
		status, body, header := h.doRaw(t, http.MethodGet, path, token, nil, "")
		requireStatus(t, status, http.StatusOK, body)
		if got := header.Get("Content-Type"); got != "image/png" {
			t.Fatalf("page %d content-type=%q", page, got)
		}
		if !strings.HasPrefix(body, "\x89PNG") {
			t.Fatalf("page %d is not a PNG", page)
		}
	}

	// Out of range and unknown uploads must not leak anything.
	for _, path := range []string{
		fmt.Sprintf("/api/app/split/page?upload_id=%s&page=4", uploadID),
		fmt.Sprintf("/api/app/split/page?upload_id=%s&page=0", uploadID),
		"/api/app/split/page?upload_id=missing&page=1",
	} {
		status, raw := h.doJSON(t, http.MethodGet, path, token, nil)
		if status != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", path, status, raw)
		}
	}
}

func TestSplitCreatesOneDocumentPerPart(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	pdf := splitPDF(t, 5)
	uploadID := h.stageSplitUpload(t, token, pdf, "scan.pdf")

	result := h.runSplit(t, token, uploadID, []map[string]int{
		pageRange(1, 1),
		pageRange(2, 3),
		pageRange(4, 5),
	})
	if intFromAny(result["created"]) != 3 {
		t.Fatalf("created=%v result=%+v", result["created"], result)
	}
	if intFromAny(result["failed"]) != 0 || intFromAny(result["skipped_duplicates"]) != 0 {
		t.Fatalf("unexpected skips/failures: %+v", result)
	}

	ids := documentIDsFrom(t, result)
	if len(ids) != 3 {
		t.Fatalf("document_ids=%v", ids)
	}
	t.Cleanup(func() { h.settleDocuments(t, ids...) })

	names := make([]string, 0, len(ids))
	for _, id := range ids {
		doc := h.getDocument(t, token, id)
		names = append(names, jsonGetString(doc, "file"))
	}
	// PocketBase sanitizes the stored file name and appends a random suffix, so
	// the assertion is on the page range the part was named after.
	for _, want := range []string{"scan_page_1", "scan_pages_2_3", "scan_pages_4_5"} {
		if !strings.Contains(strings.Join(names, " "), want) {
			t.Fatalf("expected a part named %q among %v", want, names)
		}
	}

	// The upload is consumed: the same id cannot be split again.
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/split", token, map[string]any{
		"upload_id": uploadID,
		"parts":     []map[string]int{pageRange(1, 5)},
	})
	if status != http.StatusNotFound {
		t.Fatalf("reuse status=%d body=%s", status, raw)
	}
}

func TestSplitCountsPartsAlreadyInTheLibraryAsDuplicates(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	pdf := splitPDF(t, 4)

	first := h.runSplit(t, token, h.stageSplitUpload(t, token, pdf, "scan.pdf"), []map[string]int{
		pageRange(1, 2),
		pageRange(3, 4),
	})
	firstIDs := documentIDsFrom(t, first)
	t.Cleanup(func() { h.settleDocuments(t, firstIDs...) })
	if intFromAny(first["created"]) != 2 {
		t.Fatalf("first run created=%v result=%+v", first["created"], first)
	}

	// Splitting the same scan the same way is a normal thing to do and must be
	// reported as duplicates rather than failures.
	second := h.runSplit(t, token, h.stageSplitUpload(t, token, pdf, "scan.pdf"), []map[string]int{
		pageRange(1, 2),
		pageRange(3, 4),
	})
	if intFromAny(second["skipped_duplicates"]) != 2 {
		t.Fatalf("second run duplicates=%v result=%+v", second["skipped_duplicates"], second)
	}
	if intFromAny(second["created"]) != 0 || intFromAny(second["failed"]) != 0 {
		t.Fatalf("second run should create nothing: %+v", second)
	}
}

func TestSplitRejectsPartsThatAreNotACover(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	uploadID := h.stageSplitUpload(t, token, splitPDF(t, 4), "scan.pdf")
	t.Cleanup(func() {
		h.doJSON(t, http.MethodDelete, "/api/app/split/upload?upload_id="+uploadID, token, nil)
	})

	cases := []struct {
		name  string
		parts []map[string]int
	}{
		{"gap", []map[string]int{pageRange(1, 1), pageRange(3, 4)}},
		{"overlap", []map[string]int{pageRange(1, 3), pageRange(3, 4)}},
		{"stops short", []map[string]int{pageRange(1, 2)}},
		{"past the end", []map[string]int{pageRange(1, 9)}},
		{"empty", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := h.doJSON(t, http.MethodPost, "/api/app/split", token, map[string]any{
				"upload_id": uploadID,
				"parts":     tc.parts,
			})
			if status != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", status, raw)
			}
		})
	}

	// A rejected request must leave the upload usable for a corrected one.
	status, raw := h.doJSON(t, http.MethodGet,
		fmt.Sprintf("/api/app/split/page?upload_id=%s&page=1", uploadID), token, nil)
	requireStatus(t, status, http.StatusOK, raw)
}

func TestSplitDiscardDropsTheUpload(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	uploadID := h.stageSplitUpload(t, token, splitPDF(t, 3), "scan.pdf")

	status, raw := h.doJSON(t, http.MethodDelete, "/api/app/split/upload?upload_id="+uploadID, token, nil)
	requireStatus(t, status, http.StatusOK, raw)

	status, raw = h.doJSON(t, http.MethodDelete, "/api/app/split/upload?upload_id="+uploadID, token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("second discard status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/split", token, map[string]any{
		"upload_id": uploadID,
		"parts":     []map[string]int{pageRange(1, 3)},
	})
	if status != http.StatusNotFound {
		t.Fatalf("split after discard status=%d body=%s", status, raw)
	}
}

func TestSplitUploadIsOwnerScoped(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	superToken := h.superToken(t)
	uploadID := h.stageSplitUpload(t, token, splitPDF(t, 3), "scan.pdf")
	t.Cleanup(func() {
		h.doJSON(t, http.MethodDelete, "/api/app/split/upload?upload_id="+uploadID, token, nil)
	})

	// The superuser resolves to a different owner, so the upload is invisible.
	status, raw := h.doJSON(t, http.MethodGet,
		fmt.Sprintf("/api/app/split/page?upload_id=%s&page=1", uploadID), superToken, nil)
	if status != http.StatusNotFound {
		t.Fatalf("thumbnail status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/split", superToken, map[string]any{
		"upload_id": uploadID,
		"parts":     []map[string]int{pageRange(1, 3)},
	})
	if status != http.StatusNotFound {
		t.Fatalf("split status=%d body=%s", status, raw)
	}
}

func TestSplitDetectProposesAnExactCover(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	uploadID := h.stageSplitUpload(t, token, splitPDF(t, 4), "scan.pdf")
	t.Cleanup(func() {
		h.doJSON(t, http.MethodDelete, "/api/app/split/upload?upload_id="+uploadID, token, nil)
	})

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/split/detect", token, map[string]any{
		"upload_id": uploadID,
	})
	requireStatus(t, status, http.StatusAccepted, raw)
	jobID, _ := decodeJSONMap(t, raw)["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id in %s", raw)
	}

	result := h.pollSplitJob(t, token, "/api/app/split/detect/status", jobID)
	if result["text_source"] != "pdf" {
		t.Fatalf("text_source=%v result=%+v", result["text_source"], result)
	}

	parts, _ := result["parts"].([]any)
	// The mock proposes 1-1 and 2-2 for a 4 page file; the trailing pages the
	// model forgot must be covered rather than dropped.
	if len(parts) != 3 {
		t.Fatalf("parts=%+v want 3", parts)
	}
	next := 1
	for i, item := range parts {
		part, _ := item.(map[string]any)
		if intFromAny(part["from"]) != next {
			t.Fatalf("part %d starts at %v, want %d: %+v", i, part["from"], next, parts)
		}
		next = intFromAny(part["to"]) + 1
	}
	if next != 5 {
		t.Fatalf("parts end at page %d, want 4: %+v", next-1, parts)
	}
	if parts[0].(map[string]any)["title"] != "Acme Plumbing Invoice" {
		t.Fatalf("first part title=%v", parts[0])
	}
}

func TestSplitDetectRejectsAnUnknownUpload(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/split/detect", token, map[string]any{
		"upload_id": "missing",
	})
	if status != http.StatusNotFound {
		t.Fatalf("status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/split/status?job_id=missing", token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("split status poll=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/split/detect/status?job_id=missing", token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("detect status poll=%d body=%s", status, raw)
	}
}
