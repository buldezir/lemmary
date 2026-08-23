package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// MaxSweepLimit mirrors reprocess.MaxLimit; the endpoint clamps to it.
const MaxSweepLimit = 1000

type reprocessResult struct {
	Queued    int `json:"queued"`
	Skipped   int `json:"skipped"`
	Remaining int `json:"remaining"`
}

func decodeReprocess(t testing.TB, raw string) reprocessResult {
	t.Helper()
	var result reprocessResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("decode reprocess: %v body %s", err, raw)
	}
	return result
}

// failDocument drives an uploaded document to the state a real failure leaves
// behind: the create-triggered job is failed and the document is failed. Its
// ocr_text is set (or cleared) so the auto step choice can be asserted.
func (h *Harness) failDocument(t testing.TB, documentID, ocrText string) {
	t.Helper()
	h.settleDocuments(t, documentID)

	doc, err := h.App.FindRecordById("documents", documentID)
	if err != nil {
		t.Fatalf("load document %s: %v", documentID, err)
	}
	doc.Set("ocr_text", ocrText)
	doc.Set("processing_status", models.DocStatusFailed)
	if err := h.App.Save(doc); err != nil {
		t.Fatalf("fail document %s: %v", documentID, err)
	}
}

// jobsFor returns the document's jobs, newest first.
func (h *Harness) jobsFor(t testing.TB, documentID string) []map[string]any {
	t.Helper()
	jobs, err := h.App.FindRecordsByFilter(
		"processing_jobs",
		"document = {:docId}",
		"-created",
		50,
		0,
		map[string]any{"docId": documentID},
	)
	if err != nil {
		t.Fatalf("list jobs for %s: %v", documentID, err)
	}
	out := make([]map[string]any, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, map[string]any{
			"id":     job.Id,
			"status": job.GetString("status"),
			"steps":  jobSteps(t, job),
		})
	}
	return out
}

// jobSteps reads the job's steps JSON field. A JSON field comes back as
// types.JSONRaw rather than []any, so round-trip it instead of type-asserting.
func jobSteps(t testing.TB, job *core.Record) []string {
	t.Helper()
	data, err := json.Marshal(job.Get("steps"))
	if err != nil {
		t.Fatalf("marshal steps of job %s: %v", job.Id, err)
	}
	var steps []string
	if err := json.Unmarshal(data, &steps); err != nil {
		t.Fatalf("unmarshal steps of job %s from %s: %v", job.Id, data, err)
	}
	return steps
}

func (h *Harness) uploadFailed(t testing.TB, token, ocrText string) string {
	t.Helper()
	rec := h.uploadDocument(t, token, h.UserID, fixturePath("sample.png"))
	id := jsonGetString(rec, "id")
	t.Cleanup(func() {
		_, _ = h.doJSON(t, http.MethodDelete, "/api/collections/documents/records/"+id, token, nil)
	})
	h.failDocument(t, id, ocrText)
	return id
}

// A failed document is requeued as a NEW pending job; the failed job stays as
// history, because processing_jobs has no update rule to reopen it.
func TestReprocessFailedQueuesNewJob(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	docID := h.uploadFailed(t, token, "")
	before := h.jobsFor(t, docID)
	if len(before) == 0 {
		t.Fatal("expected the upload to have created a job")
	}
	oldJobID := before[0]["id"]

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"document_ids": []string{docID},
	})
	requireStatus(t, status, http.StatusOK, raw)

	result := decodeReprocess(t, raw)
	if result.Queued != 1 || result.Skipped != 0 {
		t.Fatalf("queued=%d skipped=%d, want 1/0 body %s", result.Queued, result.Skipped, raw)
	}

	after := h.jobsFor(t, docID)
	if len(after) != len(before)+1 {
		t.Fatalf("expected one new job, had %d now %d", len(before), len(after))
	}
	if after[0]["id"] == oldJobID {
		t.Fatalf("newest job is still the old one %v", oldJobID)
	}
	// The failed job must survive as history.
	foundOld := false
	for _, job := range after {
		if job["id"] == oldJobID {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatalf("the failed job %v was removed; jobs=%v", oldJobID, after)
	}

	// The document must leave "failed" so the UI stops offering it.
	doc := h.getDocument(t, token, docID)
	if got := jsonGetString(doc, "processing_status"); got == models.DocStatusFailed {
		t.Fatalf("document is still failed after requeue: %v", doc)
	}

	h.settleDocuments(t, docID)
}

// Auto mode must not re-pay for OCR when the failed run already produced text.
func TestReprocessFailedAutoModePicksSteps(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	withText := h.uploadFailed(t, token, "text that survived the failed run")
	withoutText := h.uploadFailed(t, token, "")

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"document_ids": []string{withText, withoutText},
		"mode":         "auto",
	})
	requireStatus(t, status, http.StatusOK, raw)

	result := decodeReprocess(t, raw)
	if result.Queued != 2 {
		t.Fatalf("queued=%d, want 2 body %s", result.Queued, raw)
	}

	requireNewestJobSteps(t, h, withText, models.ExtractionPipelineSteps)
	requireNewestJobSteps(t, h, withoutText, models.FullPipelineSteps)

	h.settleDocuments(t, withText, withoutText)
}

// An explicit mode overrides the per-document guess.
func TestReprocessFailedExplicitModeOverridesAuto(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	docID := h.uploadFailed(t, token, "text that survived the failed run")

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"document_ids": []string{docID},
		"mode":         "full",
	})
	requireStatus(t, status, http.StatusOK, raw)
	requireNewestJobSteps(t, h, docID, models.FullPipelineSteps)

	h.settleDocuments(t, docID)
}

func TestReprocessFailedRejectsUnknownMode(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"mode": "sideways",
	})
	requireStatus(t, status, http.StatusBadRequest, raw)
}

func TestReprocessFailedRequiresAuth(t *testing.T) {
	h := StartShared(t)

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", "", map[string]any{
		"limit": 1,
	})
	requireStatus(t, status, http.StatusUnauthorized, raw)
}

// limit caps one batch, so a click cannot commit unbounded AI spend. The
// leftover document stays failed and is picked up by the next batch.
func TestReprocessFailedLimitCapsTheBatch(t *testing.T) {
	h := StartShared(t)

	token, _ := h.newFailedUser(t, "limit", 3)

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"limit": 2,
	})
	requireStatus(t, status, http.StatusOK, raw)

	result := decodeReprocess(t, raw)
	if result.Queued != 2 {
		t.Fatalf("queued=%d, want exactly the limit of 2; body %s", result.Queued, raw)
	}
	if result.Remaining != 1 {
		t.Fatalf("remaining=%d, want the 1 document left unqueued; body %s", result.Remaining, raw)
	}

	// The next batch clears the remainder.
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"limit": 2,
	})
	requireStatus(t, status, http.StatusOK, raw)

	result = decodeReprocess(t, raw)
	if result.Queued != 1 || result.Remaining != 0 {
		t.Fatalf("second batch queued=%d remaining=%d, want 1/0; body %s", result.Queued, result.Remaining, raw)
	}
}

// A document that is already queued must not be queued twice, so a stale
// selection in the UI cannot double-spend.
func TestReprocessFailedSkipsAlreadyQueued(t *testing.T) {
	h := StartShared(t)
	token := h.userToken(t)

	docID := h.uploadFailed(t, token, "")

	status, raw := h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"document_ids": []string{docID},
	})
	requireStatus(t, status, http.StatusOK, raw)

	// Immediately resubmit the same selection.
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"document_ids": []string{docID},
	})
	requireStatus(t, status, http.StatusOK, raw)

	result := decodeReprocess(t, raw)
	if result.Queued+result.Skipped != 1 {
		t.Fatalf("queued=%d skipped=%d, want one or the other; body %s", result.Queued, result.Skipped, raw)
	}

	h.settleDocuments(t, docID)
}

// newFailedUser creates a fresh user owning exactly count failed documents.
//
// An unqualified sweep takes the owner's oldest failed documents first, so a
// sweep run as the shared e2e user would requeue whatever earlier tests left
// behind and let the pipeline rewrite their titles. Giving each sweep test its
// own user keeps the blast radius to documents that test created, and makes the
// queued/remaining counts exact rather than "at least".
func (h *Harness) newFailedUser(t testing.TB, label string, count int) (token string, documentIDs []string) {
	t.Helper()
	email := fmt.Sprintf("reprocess-%s-%d@lemmary.local", label, time.Now().UnixNano())
	password := "reprocesspassword123"
	userID, err := createAuthRecord(h.App, "users", email, password)
	if err != nil {
		t.Fatalf("create user %s: %v", label, err)
	}
	t.Cleanup(func() {
		if rec, err := h.App.FindRecordById("users", userID); err == nil {
			_ = h.App.Delete(rec)
		}
	})

	token = h.authWithPassword(t, "users", email, password).Token
	documentIDs = make([]string, 0, count)
	for i := 0; i < count; i++ {
		rec := h.uploadDocumentBytes(t, token, userID,
			[]byte(fmt.Sprintf("reprocess %s fixture %d %d", label, i, time.Now().UnixNano())),
			fmt.Sprintf("reprocess-%s-%d.txt", label, i),
		)
		id := jsonGetString(rec, "id")
		h.failDocument(t, id, "")
		documentIDs = append(documentIDs, id)
	}
	t.Cleanup(func() { h.settleDocuments(t, documentIDs...) })
	return token, documentIDs
}

// The sweep is owner-scoped: another user's failed documents are neither queued
// by name nor reached by an unqualified sweep.
//
// Both users are freshly created and own exactly one failed document each, so
// the unqualified sweep below cannot requeue documents other tests rely on -
// a background pipeline run would rewrite their titles and metadata.
func TestReprocessFailedIsOwnerScoped(t *testing.T) {
	h := StartShared(t)

	token, _ := h.newFailedUser(t, "owner", 1)
	_, otherDocs := h.newFailedUser(t, "other", 1)
	otherDocID := otherDocs[0]
	otherJobsBefore := len(h.jobsFor(t, otherDocID))

	// A named selection of someone else's document is refused, not queued.
	status, raw := h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"document_ids": []string{otherDocID},
	})
	requireStatus(t, status, http.StatusOK, raw)

	result := decodeReprocess(t, raw)
	if result.Queued != 0 || result.Skipped != 1 {
		t.Fatalf("queued=%d skipped=%d, want 0/1 for another user's document; body %s", result.Queued, result.Skipped, raw)
	}
	// remaining counts only the caller's own failed documents.
	if result.Remaining != 1 {
		t.Fatalf("remaining=%d, want 1 (the caller's own failed document only); body %s", result.Remaining, raw)
	}

	// The unqualified sweep must not reach it either.
	status, raw = h.doJSON(t, http.MethodPost, "/api/app/documents/reprocess-failed", token, map[string]any{
		"limit": MaxSweepLimit,
	})
	requireStatus(t, status, http.StatusOK, raw)

	result = decodeReprocess(t, raw)
	if result.Queued != 1 {
		t.Fatalf("queued=%d, want exactly the caller's own 1 failed document; body %s", result.Queued, raw)
	}
	if result.Remaining != 0 {
		t.Fatalf("remaining=%d, want 0; body %s", result.Remaining, raw)
	}

	if got := len(h.jobsFor(t, otherDocID)); got != otherJobsBefore {
		t.Fatalf("other user's document gained jobs: had %d now %d", otherJobsBefore, got)
	}
	otherDoc, err := h.App.FindRecordById("documents", otherDocID)
	if err != nil {
		t.Fatalf("reload other document: %v", err)
	}
	if got := otherDoc.GetString("processing_status"); got != models.DocStatusFailed {
		t.Fatalf("other user's document status changed to %q", got)
	}
}

func requireNewestJobSteps(t testing.TB, h *Harness, documentID string, want []string) {
	t.Helper()
	jobs := h.jobsFor(t, documentID)
	if len(jobs) == 0 {
		t.Fatalf("no jobs for document %s", documentID)
	}
	got, _ := jobs[0]["steps"].([]string)
	if len(got) != len(want) {
		t.Fatalf("document %s newest job steps=%v, want %v", documentID, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("document %s newest job steps=%v, want %v", documentID, got, want)
		}
	}
}
