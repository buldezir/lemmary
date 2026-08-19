package e2e

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"paperless-go/backend/internal/worker"
)

func TestPipelineCompletesWithMocks(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.png"))
	id := jsonGetString(rec, "id")

	doc := h.waitDocumentStatus(t, token, id, "completed", "needs_review")
	ocr := jsonGetString(doc, "ocr_text")
	if !strings.Contains(ocr, "Acme Plumbing") {
		t.Fatalf("expected OCR text, got %q", ocr)
	}
	title := jsonGetString(doc, "title")
	if title == "" {
		t.Fatal("expected extracted title")
	}
	if !strings.Contains(strings.ToLower(title), "acme") && !strings.Contains(strings.ToLower(title), "invoice") {
		t.Fatalf("unexpected title %q", title)
	}

	// Job should exist and be completed.
	status, raw := h.doJSON(t, http.MethodGet, `/api/collections/processing_jobs/records?filter=document="`+id+`"&sort=-created&perPage=1`, token, nil)
	requireStatus(t, status, http.StatusOK, raw)
	jobs := mustDecodeList(t, raw)
	if len(jobs) == 0 {
		t.Fatal("expected processing job")
	}
	jobStatus := jsonGetString(jobs[0], "status")
	if jobStatus != "completed" && jobStatus != "needs_review" {
		t.Fatalf("job status=%q body=%s", jobStatus, raw)
	}
}

func TestPipelineReprocess(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.png"))
	id := jsonGetString(rec, "id")
	_ = h.waitDocumentStatus(t, token, id, "completed", "needs_review")

	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/processing_jobs/records", token, map[string]any{
		"document":    id,
		"status":      "pending",
		"steps":       []string{"extract_metadata", "apply_metadata"},
		"force_steps": []string{"extract_metadata", "apply_metadata"},
	})
	requireStatus(t, status, http.StatusOK, raw)

	var job map[string]any
	if err := json.Unmarshal([]byte(raw), &job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	jobID := jsonGetString(job, "id")

	deadlineOK := false
	for i := 0; i < 100; i++ {
		status, raw = h.doJSON(t, http.MethodGet, "/api/collections/processing_jobs/records/"+jobID, token, nil)
		requireStatus(t, status, http.StatusOK, raw)
		_ = json.Unmarshal([]byte(raw), &job)
		st := jsonGetString(job, "status")
		if st == "completed" || st == "needs_review" || st == "failed" {
			deadlineOK = true
			if st == "failed" {
				t.Fatalf("reprocess failed: %s", raw)
			}
			break
		}
		h.waitDocumentStatus(t, token, id, "completed", "needs_review", "processing", "pending", "failed")
	}
	if !deadlineOK {
		t.Fatalf("reprocess job did not finish: %v", job)
	}
}

func TestExtractionPromptIncludesExistingNamedEntities(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)
	suffix := strings.ReplaceAll(t.Name(), "/", "-")
	corrName := "Amazon EU S.à r.l. " + suffix
	typeName := "Credit Note " + suffix
	otherCorr := "OtherUserCorr-" + suffix

	status, raw := h.doJSON(t, http.MethodPost, "/api/collections/correspondents/records", token, map[string]any{
		"name":          corrName,
		"name_original": corrName,
		"user":          h.UserID,
	})
	requireStatus(t, status, http.StatusOK, raw)
	corrID := jsonGetString(mustDecodeMap(t, raw), "id")
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/correspondents/records/"+corrID, token, nil)
	})

	status, raw = h.doJSON(t, http.MethodPost, "/api/collections/document_types/records", token, map[string]any{
		"name":          typeName,
		"name_original": typeName,
		"user":          h.UserID,
	})
	requireStatus(t, status, http.StatusOK, raw)
	typeID := jsonGetString(mustDecodeMap(t, raw), "id")
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/document_types/records/"+typeID, token, nil)
	})

	otherEmail := "catalog-other-" + suffix + "@paperless.local"
	otherPass := "otherpassword123"
	status, raw = h.doJSON(t, http.MethodPost, "/api/collections/users/records", h.superToken(t), map[string]any{
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
			t.Fatalf("create other user: API %s app %v", formatErr(status, raw), err)
		}
	}
	idOther, createdOther, err := worker.EnsureNamedEntity(h.App, "correspondents", otherID, otherCorr, otherCorr)
	if err != nil || !createdOther {
		t.Fatalf("create other user correspondent: id=%s created=%v err=%v", idOther, createdOther, err)
	}
	t.Cleanup(func() {
		if rec, err := h.App.FindRecordById("correspondents", idOther); err == nil {
			_ = h.App.Delete(rec)
		}
	})

	h.Mocks.ResetOpenAIBodies()
	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.png"))
	id := jsonGetString(rec, "id")
	_ = h.waitDocumentStatus(t, token, id, "completed", "needs_review")

	foundCorr, foundType, leaked := false, false, false
	for _, body := range h.Mocks.LastOpenAIBodies() {
		if strings.Contains(body, corrName) && strings.Contains(body, "untrusted user data") {
			foundCorr = true
		}
		if strings.Contains(body, typeName) && strings.Contains(strings.ToLower(body), "existing document types") {
			foundType = true
		}
		if strings.Contains(body, otherCorr) {
			leaked = true
		}
	}
	if !foundCorr || !foundType {
		t.Fatalf("expected existing correspondent %q and document type %q in extraction prompt, bodies=%v", corrName, typeName, h.Mocks.LastOpenAIBodies())
	}
	if leaked {
		t.Fatalf("other user's correspondent leaked into extraction prompt: %v", h.Mocks.LastOpenAIBodies())
	}
}
