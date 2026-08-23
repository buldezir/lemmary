package e2e

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"testing"
	"time"
)

type archiveFile struct {
	name string
	body []byte
}

// amazonExportZip builds a zip shaped like the Amazon "Your Orders" export.
func amazonExportZip(t *testing.T, extra ...archiveFile) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, file := range extra {
		f, err := w.Create(file.name)
		if err != nil {
			t.Fatalf("create %s: %v", file.name, err)
		}
		if _, err := f.Write(file.body); err != nil {
			t.Fatalf("write %s: %v", file.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func invoicePath(folder string, n int) string {
	return fmt.Sprintf("Your Orders/Additional Data/Retail.TransactionalInvoicing.%s/%d.pdf", folder, n)
}

func (h *Harness) uploadAmazonArchive(t *testing.T, token string, archive []byte, fileName string) (int, string) {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", fileName)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := part.Write(archive); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	status, raw, _ := h.doRaw(t, http.MethodPost, "/api/app/import/amazon/upload", token, &body, w.FormDataContentType())
	return status, raw
}

func decodeJSONMap(t *testing.T, raw string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("decode: %v body %s", err, raw)
	}
	return out
}

func runAmazonImport(t *testing.T, h *Harness, token, uploadID string) map[string]any {
	t.Helper()
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/amazon", token, map[string]any{
		"upload_id": uploadID,
	})
	requireStatus(t, status, http.StatusAccepted, raw)
	jobID, _ := decodeJSONMap(t, raw)["job_id"].(string)
	if jobID == "" {
		t.Fatalf("missing job_id in %s", raw)
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		status, raw := h.doJSON(t, http.MethodGet, "/api/app/import/amazon/status?job_id="+jobID, token, nil)
		requireStatus(t, status, http.StatusOK, raw)
		payload := decodeJSONMap(t, raw)
		switch payload["status"] {
		case "completed":
			result, _ := payload["result"].(map[string]any)
			if result == nil {
				t.Fatalf("completed without result: %s", raw)
			}
			progress, _ := payload["progress"].(map[string]any)
			if intFromAny(progress["done"]) != intFromAny(progress["total"]) {
				t.Fatalf("progress not finished: %s", raw)
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

func (h *Harness) documentsByChecksum(t *testing.T, token, checksum string) []map[string]any {
	t.Helper()
	status, raw := h.doJSON(t, http.MethodGet,
		`/api/collections/documents/records?filter=checksum="`+checksum+`"&perPage=50`, token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	return mustDecodeList(t, raw)
}

func TestImportAmazonRequiresAuth(t *testing.T) {
	h := StartShared(t)
	archive := amazonExportZip(t, archiveFile{invoicePath("2.1", 1), []byte("%PDF-unauth")})

	status, raw := h.uploadAmazonArchive(t, "", archive, "orders.zip")
	if status != http.StatusUnauthorized {
		t.Fatalf("upload status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/import/amazon", "", map[string]any{"upload_id": "x"})
	if status != http.StatusUnauthorized {
		t.Fatalf("import status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/import/amazon/status?job_id=x", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("status poll=%d body=%s", status, raw)
	}
}

func TestImportAmazonRejectsUnusableArchives(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	status, raw := h.uploadAmazonArchive(t, token, []byte("this is not a zip"), "orders.zip")
	if status != http.StatusBadRequest {
		t.Fatalf("non-zip status=%d body=%s", status, raw)
	}

	csvOnly := amazonExportZip(t,
		archiveFile{"Your Orders/Your Amazon Orders/Order History.csv", []byte("order,history")},
		archiveFile{"Your Orders/Your Amazon Orders/Media/YourOrders.PhotoOnDelivery/media/a.jpeg", []byte("jpeg")},
	)
	status, raw = h.uploadAmazonArchive(t, token, csvOnly, "orders.zip")
	if status != http.StatusBadRequest {
		t.Fatalf("pdf-less status=%d body=%s", status, raw)
	}
	requireContains(t, raw, "No PDF files")
}

func TestImportAmazonPreviewThenConfirm(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	first := []byte("%PDF-amazon-invoice-one-" + t.Name())
	second := []byte("%PDF-amazon-invoice-two-" + t.Name())
	archive := amazonExportZip(t,
		archiveFile{"Your Orders/Your Amazon Orders/Order History.csv", []byte("order,history")},
		archiveFile{"Your Orders/Your Amazon Orders/Media/YourOrders.PhotoOnDelivery/media/a.jpeg", []byte("jpeg")},
		archiveFile{"__MACOSX/Your Orders/._ignored.pdf", []byte("junk")},
		archiveFile{invoicePath("2.1", 1), first},
		archiveFile{invoicePath("2.1", 2), second},
		// Same bytes as invoice 1 under the second folder: an in-archive repeat.
		archiveFile{invoicePath("2.2", 1), first},
	)

	status, raw := h.uploadAmazonArchive(t, token, archive, "Your Orders.zip")
	requireStatus(t, status, http.StatusOK, raw)
	preview := decodeJSONMap(t, raw)

	uploadID, _ := preview["upload_id"].(string)
	if uploadID == "" {
		t.Fatalf("missing upload_id: %s", raw)
	}
	if got := intFromAny(preview["pdf_count"]); got != 3 {
		t.Fatalf("pdf_count=%d want 3 body=%s", got, raw)
	}
	if got := intFromAny(preview["importable_count"]); got != 2 {
		t.Fatalf("importable_count=%d want 2 body=%s", got, raw)
	}
	if got := intFromAny(preview["duplicate_count"]); got != 1 {
		t.Fatalf("duplicate_count=%d want 1 body=%s", got, raw)
	}
	if got := intFromAny(preview["ignored_count"]); got != 2 {
		t.Fatalf("ignored_count=%d want 2 (csv, jpeg) body=%s", got, raw)
	}

	// Nothing may be created before the user confirms.
	if docs := h.documentsByChecksum(t, token, sha256Hex(first)); len(docs) != 0 {
		t.Fatalf("preview created %d documents", len(docs))
	}

	result := runAmazonImport(t, h, token, uploadID)
	if got := intFromAny(result["imported"]); got != 2 {
		t.Fatalf("imported=%d want 2 result=%v", got, result)
	}
	if got := intFromAny(result["skipped_duplicates"]); got != 1 {
		t.Fatalf("skipped_duplicates=%d want 1 result=%v", got, result)
	}
	if got := intFromAny(result["failed"]); got != 0 {
		t.Fatalf("failed=%d errors=%v", got, result["errors"])
	}

	docs := h.documentsByChecksum(t, token, sha256Hex(first))
	if len(docs) != 1 {
		t.Fatalf("expected 1 document for the first invoice, got %d", len(docs))
	}
	if owner := jsonGetString(docs[0], "user"); owner != h.UserID {
		t.Fatalf("owner=%q want %q", owner, h.UserID)
	}
	if file := jsonGetString(docs[0], "file"); file == "" {
		t.Fatalf("document has no stored file: %v", docs[0])
	}
	if len(h.documentsByChecksum(t, token, sha256Hex(second))) != 1 {
		t.Fatal("expected the second invoice to be imported")
	}
	h.settleDocuments(t, jsonGetString(docs[0], "id"))

	// The upload is consumed once imported.
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/import/amazon", token, map[string]any{"upload_id": uploadID})
	if status != http.StatusNotFound {
		t.Fatalf("reimport status=%d body=%s", status, raw)
	}

	// Re-uploading the same archive now previews every invoice as known.
	status, raw = h.uploadAmazonArchive(t, token, archive, "Your Orders.zip")
	requireStatus(t, status, http.StatusOK, raw)
	preview = decodeJSONMap(t, raw)
	if got := intFromAny(preview["importable_count"]); got != 0 {
		t.Fatalf("second preview importable_count=%d want 0 body=%s", got, raw)
	}
	if got := intFromAny(preview["duplicate_count"]); got != 3 {
		t.Fatalf("second preview duplicate_count=%d want 3 body=%s", got, raw)
	}
	files, _ := preview["files"].([]any)
	if len(files) != 3 {
		t.Fatalf("files=%d want 3", len(files))
	}
	firstFile, _ := files[0].(map[string]any)
	if jsonGetString(firstFile, "duplicate_of") == "" {
		t.Fatalf("expected duplicate_of on a known invoice: %v", firstFile)
	}

	secondUpload, _ := preview["upload_id"].(string)
	result = runAmazonImport(t, h, token, secondUpload)
	if intFromAny(result["imported"]) != 0 || intFromAny(result["skipped_duplicates"]) != 3 {
		t.Fatalf("re-import result=%v", result)
	}
}

func TestImportAmazonDiscardStagedArchive(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	payload := []byte("%PDF-amazon-discard-" + t.Name())
	archive := amazonExportZip(t, archiveFile{invoicePath("2.1", 9), payload})

	status, raw := h.uploadAmazonArchive(t, token, archive, "orders.zip")
	requireStatus(t, status, http.StatusOK, raw)
	uploadID, _ := decodeJSONMap(t, raw)["upload_id"].(string)

	status, raw = h.doJSON(t, http.MethodDelete, "/api/app/import/amazon/upload?upload_id="+uploadID, token, nil)
	requireStatus(t, status, http.StatusOK, raw)

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/import/amazon", token, map[string]any{"upload_id": uploadID})
	if status != http.StatusNotFound {
		t.Fatalf("import after discard status=%d body=%s", status, raw)
	}
	if docs := h.documentsByChecksum(t, token, sha256Hex(payload)); len(docs) != 0 {
		t.Fatalf("discarded archive created %d documents", len(docs))
	}
}

func TestImportAmazonUploadIsOwnerScoped(t *testing.T) {
	h := StartShared(t)
	userToken := h.userToken(t)
	adminToken := h.adminUserToken(t)

	archive := amazonExportZip(t, archiveFile{invoicePath("2.1", 3), []byte("%PDF-amazon-scoped-" + t.Name())})
	status, raw := h.uploadAmazonArchive(t, userToken, archive, "orders.zip")
	requireStatus(t, status, http.StatusOK, raw)
	uploadID, _ := decodeJSONMap(t, raw)["upload_id"].(string)

	status, raw = h.doJSON(t, http.MethodPost, "/api/app/import/amazon", adminToken, map[string]any{"upload_id": uploadID})
	if status != http.StatusNotFound {
		t.Fatalf("cross-user import status=%d body=%s", status, raw)
	}
}

func TestImportAmazonValidation(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/import/amazon", token, map[string]any{"upload_id": ""})
	if status != http.StatusBadRequest {
		t.Fatalf("empty upload_id status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/import/amazon/status", token, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("missing job_id status=%d body=%s", status, raw)
	}
	status, raw = h.doJSON(t, http.MethodGet, "/api/app/import/amazon/status?job_id=nope", token, nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown job status=%d body=%s", status, raw)
	}
	status, raw, _ = h.doRaw(t, http.MethodPost, "/api/app/import/amazon/upload", token, bytes.NewReader(nil), "application/json")
	if status != http.StatusBadRequest {
		t.Fatalf("non-multipart status=%d body=%s", status, raw)
	}
}
