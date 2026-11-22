package worker

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/limits"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/pdftool"
	"lemmary/backend/internal/pdftool/testpdf"
)

func makePDFDocumentWithJob(t *testing.T, app core.App, userID string, pdf []byte, steps []string) (documentID, jobID string) {
	t.Helper()
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	document := core.NewRecord(documents)
	document.Set("user", userID)
	file, err := filesystem.NewFileFromBytes(pdf, "statement.pdf")
	if err != nil {
		t.Fatal(err)
	}
	document.Set("file", file)
	if err := app.Save(document); err != nil {
		t.Fatalf("save document: %v", err)
	}
	job := makeOCRJob(t, app, document.Id)
	job.Set("steps", steps)
	if err := app.Save(job); err != nil {
		t.Fatal(err)
	}
	return document.Id, job.Id
}

// The provider is never called and the job is not retried: the document waits
// in password_required with the step naming why, not a raw PDFium error.
func TestUnlockStepStopsAPasswordProtectedPDF(t *testing.T) {
	if _, err := exec.LookPath("qpdf"); err != nil {
		t.Skip("qpdf not installed")
	}
	locked, err := testpdf.Encrypt(testpdf.Multipage(2), "secret", "secret")
	if err != nil {
		t.Fatal(err)
	}
	ownerOnly, err := testpdf.Encrypt(testpdf.Multipage(2), "", "owner")
	if err != nil {
		t.Fatal(err)
	}

	app := bootAppForEnqueue(t)
	userID := makeUserForDrain(t, app, "unlock@example.test")
	steps := []string{models.StepUnlock, models.StepOCR}
	lockedDoc, lockedJob := makePDFDocumentWithJob(t, app, userID, locked, steps)
	openDoc, openJob := makePDFDocumentWithJob(t, app, userID, ownerOnly, steps)
	// A job queued before the unlock step existed gets it all the same.
	oldDoc, oldJob := makePDFDocumentWithJob(t, app, userID, locked, []string{models.StepOCR})

	provider := &slowOCR{}
	newDrainProcessor(app, 1, provider).fanOut()

	if _, calls := provider.stats(); calls != 1 {
		t.Fatalf("provider called %d times, want once for the owner-only PDF", calls)
	}
	for _, c := range []struct{ doc, job, docStatus, jobStatus string }{
		{lockedDoc, lockedJob, models.DocStatusPasswordRequired, models.JobStatusFailed},
		{oldDoc, oldJob, models.DocStatusPasswordRequired, models.JobStatusFailed},
		{openDoc, openJob, models.DocStatusCompleted, models.JobStatusCompleted},
	} {
		document, err := app.FindRecordById("documents", c.doc)
		if err != nil {
			t.Fatal(err)
		}
		job, err := app.FindRecordById("processing_jobs", c.job)
		if err != nil {
			t.Fatal(err)
		}
		if got := document.GetString("processing_status"); got != c.docStatus {
			t.Errorf("document status = %q, want %q", got, c.docStatus)
		}
		if got := job.GetString("status"); got != c.jobStatus {
			t.Errorf("job status = %q, want %q (error %q, runs %s)", got, c.jobStatus, job.GetString("error"), job.GetString("step_runs"))
		}
	}

	runs, err := parseStepRuns(mustFindJob(t, app, oldJob))
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[0].Name != models.StepUnlock {
		t.Fatalf("step runs = %+v; want unlock then OCR", runs)
	}
	if runs[0].Status != models.StepStatusFailed || runs[0].Attempts != 1 || runs[1].Status != models.StepStatusPending {
		t.Fatalf("step runs = %+v; want unlock failed once and OCR never started", runs)
	}
}

func mustFindJob(t *testing.T, app core.App, id string) *core.Record {
	t.Helper()
	job, err := app.FindRecordById("processing_jobs", id)
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestWithUnlockStep(t *testing.T) {
	for _, c := range []struct{ in, want []string }{
		{[]string{models.StepOCR, models.StepEmbed}, []string{models.StepUnlock, models.StepOCR, models.StepEmbed}},
		{[]string{models.StepPreview}, []string{models.StepUnlock, models.StepPreview}},
		{models.FullPipelineSteps, models.FullPipelineSteps},
		{models.ExtractionPipelineSteps, models.ExtractionPipelineSteps},
	} {
		if got := withUnlockStep(c.in); !slices.Equal(got, c.want) {
			t.Errorf("withUnlockStep(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestUnlock(t *testing.T) {
	if _, err := exec.LookPath("qpdf"); err != nil {
		t.Skip("qpdf not installed")
	}
	locked, err := testpdf.Encrypt(testpdf.Multipage(3), "pässwörd", "owner")
	if err != nil {
		t.Fatal(err)
	}
	app := bootAppForEnqueue(t)
	limits.Register(app, limits.Limits{})
	userID := makeUserForDrain(t, app, "unlock-endpoint@example.test")
	documentID, jobID := makePDFDocumentWithJob(t, app, userID, locked, models.FullPipelineSteps)
	document, err := app.FindRecordById("documents", documentID)
	if err != nil {
		t.Fatal(err)
	}

	if err := Unlock(context.Background(), app, document, "pässwörd"); !errors.Is(err, ErrNotLocked) {
		t.Fatalf("Unlock before the pipeline found the password: %v, want ErrNotLocked", err)
	}

	newDrainProcessor(app, 1, &slowOCR{}).fanOut()
	document, err = app.FindRecordById("documents", documentID)
	if err != nil {
		t.Fatal(err)
	}
	storedName, checksum := document.GetString("file"), document.GetString("checksum")
	if got := document.GetInt("page_count"); got != 1 {
		t.Fatalf("page_count of the locked upload = %d, want the one-page fallback", got)
	}

	if err := Unlock(context.Background(), app, document, "wrong"); !errors.Is(err, pdftool.ErrIncorrectPassword) {
		t.Fatalf("wrong password: %v, want ErrIncorrectPassword", err)
	}
	document, err = app.FindRecordById("documents", documentID)
	if err != nil {
		t.Fatal(err)
	}
	if document.GetString("file") != storedName || document.GetString("processing_status") != models.DocStatusPasswordRequired {
		t.Fatalf("a wrong password changed the document: file %q, status %q", document.GetString("file"), document.GetString("processing_status"))
	}

	if err := Unlock(context.Background(), app, document, "pässwörd"); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	document, err = app.FindRecordById("documents", documentID)
	if err != nil {
		t.Fatal(err)
	}
	if document.GetString("processing_status") != models.DocStatusPending || document.GetInt("page_count") != 3 {
		t.Fatalf("after unlock: status %q, page_count %d; want pending and 3", document.GetString("processing_status"), document.GetInt("page_count"))
	}
	if document.GetString("checksum") == checksum || document.GetString("file") == storedName {
		t.Fatal("the stored file and its checksum were not replaced")
	}
	if strings.Count(document.GetString("file"), "_") != strings.Count(storedName, "_") {
		t.Fatalf("file name %q stacked another suffix onto %q", document.GetString("file"), storedName)
	}
	path, _, cleanup, err := readDocumentToTempFile(app, document)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := pdftool.PageCount(context.Background(), path); err != nil {
		t.Fatalf("the stored file still needs a password: %v", err)
	}

	jobs, err := app.FindRecordsByFilter("processing_jobs", "document = {:doc} && id != {:old}", "", 0, 0, map[string]any{"doc": documentID, "old": jobID})
	if err != nil || len(jobs) != 1 {
		t.Fatalf("expected one new job, got %d (%v)", len(jobs), err)
	}
	steps, err := parseSteps(jobs[0])
	if err != nil || !slices.Equal(steps, models.FullPipelineSteps) {
		t.Fatalf("new job steps = %v (%v), want the full pipeline", steps, err)
	}
}
