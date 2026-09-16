package appapi

import (
	"testing"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/testpb"
	// Registers the migrations that create documents and processing_jobs;
	// without them the shared schema template is empty.
	_ "lemmary/backend/migrations"
)

func bootQueueApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	return testpb.Open(t)
}

func makeQueueUser(t *testing.T, app core.App, email string) string {
	t.Helper()
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	user := core.NewRecord(users)
	user.Set("email", email)
	user.SetPassword("test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return user.Id
}

// documents.file is required, which is also the point of the discard rules:
// the file is what gets thrown away.
func makeQueueDocument(t *testing.T, app core.App, ownerID, status, ocrText string) *core.Record {
	t.Helper()
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	document := core.NewRecord(documents)
	document.Set("user", ownerID)
	document.Set("title", "Queue test")
	document.Set("processing_status", status)
	document.Set("ocr_text", ocrText)
	file, err := filesystem.NewFileFromBytes([]byte("queue test"), "queue.txt")
	if err != nil {
		t.Fatalf("build file: %v", err)
	}
	document.Set("file", file)
	if err := app.Save(document); err != nil {
		t.Fatalf("save document: %v", err)
	}
	return document
}

func makeQueueJob(t *testing.T, app core.App, documentID, status string, finished bool) *core.Record {
	t.Helper()
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatalf("processing_jobs collection: %v", err)
	}
	job := core.NewRecord(jobs)
	job.Set("document", documentID)
	job.Set("status", status)
	if finished {
		job.Set("finished_at", types.NowDateTime())
	}
	if err := app.Save(job); err != nil {
		t.Fatalf("save job: %v", err)
	}
	return job
}

func documentStatus(t *testing.T, app core.App, documentID string) string {
	t.Helper()
	document, err := app.FindRecordById("documents", documentID)
	if err != nil {
		t.Fatalf("reload document: %v", err)
	}
	return document.GetString("processing_status")
}

func TestStopJobCancelsAPendingDocument(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "stop-pending@example.test")
	document := makeQueueDocument(t, app, owner, models.DocStatusPending, "")
	job := makeQueueJob(t, app, document.Id, models.JobStatusPending, false)

	stopped, err := stopJob(app, job)
	if err != nil {
		t.Fatalf("stopJob: %v", err)
	}
	if !stopped {
		t.Fatal("stopJob reported the job as already claimed")
	}
	if got := documentStatus(t, app, document.Id); got != models.DocStatusCancelled {
		t.Fatalf("document status = %q, want %q", got, models.DocStatusCancelled)
	}

	fresh, err := app.FindRecordById("processing_jobs", job.Id)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if got := fresh.GetString("status"); got != models.JobStatusCancelled {
		t.Fatalf("job status = %q, want %q", got, models.JobStatusCancelled)
	}
	if fresh.GetString("finished_at") == "" {
		t.Fatal("cancelled job left without finished_at")
	}
}

// recoverStaleRunningJobs re-pends a job whose document is already "completed",
// and stamping cancelled over that would hand the discard sweep a processed
// document.
func TestStopJobLeavesAProcessedDocumentAlone(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "stop-completed@example.test")
	document := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "extracted text")
	job := makeQueueJob(t, app, document.Id, models.JobStatusPending, false)

	stopped, err := stopJob(app, job)
	if err != nil {
		t.Fatalf("stopJob: %v", err)
	}
	if !stopped {
		t.Fatal("stopJob reported the job as already claimed")
	}
	if got := documentStatus(t, app, document.Id); got != models.DocStatusCompleted {
		t.Fatalf("document status = %q, want it left at %q", got, models.DocStatusCompleted)
	}

	fresh, err := app.FindRecordById("processing_jobs", job.Id)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	if got := fresh.GetString("status"); got != models.JobStatusCancelled {
		t.Fatalf("job status = %q, want %q", got, models.JobStatusCancelled)
	}
}

// The worker claims a job by flipping it to running in its own transaction.
func TestStopJobDoesNotTouchAClaimedJob(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "stop-claimed@example.test")
	document := makeQueueDocument(t, app, owner, models.DocStatusProcessing, "")
	job := makeQueueJob(t, app, document.Id, models.JobStatusPending, false)

	// The claim, as the drain loop makes it, after the handler read the job.
	claimed, err := app.FindRecordById("processing_jobs", job.Id)
	if err != nil {
		t.Fatalf("reload job: %v", err)
	}
	claimed.Set("status", models.JobStatusRunning)
	if err := app.Save(claimed); err != nil {
		t.Fatalf("claim job: %v", err)
	}

	stopped, err := stopJob(app, job)
	if err != nil {
		t.Fatalf("stopJob: %v", err)
	}
	if stopped {
		t.Fatal("stopJob cancelled a job the worker had already claimed")
	}
	if got := documentStatus(t, app, document.Id); got != models.DocStatusProcessing {
		t.Fatalf("document status = %q, want it left at %q", got, models.DocStatusProcessing)
	}
}

// What the sweep is for: an import stopped before it ever ran.
func TestDiscardDocumentDeletesACancelledImport(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "discard-cancelled@example.test")
	document := makeQueueDocument(t, app, owner, models.DocStatusCancelled, "")
	// Stopping leaves a finished job on an untouched document, so a cancelled
	// job must not read as "processed".
	makeQueueJob(t, app, document.Id, models.JobStatusCancelled, true)

	deleted, err := discardDocument(app, document.Id, owner)
	if err != nil {
		t.Fatalf("discardDocument: %v", err)
	}
	if !deleted {
		t.Fatal("a cancelled, never-processed document survived the sweep")
	}
	if _, err := app.FindRecordById("documents", document.Id); err == nil {
		t.Fatal("document still readable after discard")
	}
}

// queueOne flips a completed document back to pending, so "pending" alone is
// not "never processed"; its OCR text is the cheap proof.
func TestDiscardDocumentKeepsARequeuedDocumentWithText(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "discard-requeued@example.test")
	document := makeQueueDocument(t, app, owner, models.DocStatusPending, "the text this document already has")
	makeQueueJob(t, app, document.Id, models.JobStatusPending, false)

	deleted, err := discardDocument(app, document.Id, owner)
	if err != nil {
		t.Fatalf("discardDocument: %v", err)
	}
	if deleted {
		t.Fatal("the sweep deleted a document that had already been processed")
	}
	if _, err := app.FindRecordById("documents", document.Id); err != nil {
		t.Fatalf("document gone after a sweep that reported keeping it: %v", err)
	}
}

// The backstop for a scan OCR found no words in: no text, but a finished job
// that says it went through the pipeline.
func TestDiscardDocumentKeepsARequeuedDocumentWithoutText(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "discard-requeued-empty@example.test")
	document := makeQueueDocument(t, app, owner, models.DocStatusPending, "")
	makeQueueJob(t, app, document.Id, models.JobStatusCompleted, true)
	makeQueueJob(t, app, document.Id, models.JobStatusPending, false)

	deleted, err := discardDocument(app, document.Id, owner)
	if err != nil {
		t.Fatalf("discardDocument: %v", err)
	}
	if deleted {
		t.Fatal("the sweep deleted a document with a completed job behind it")
	}
}

func TestDiscardDocumentLeavesAnotherOwnersDocument(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "discard-owner@example.test")
	other := makeQueueUser(t, app, "discard-other@example.test")
	document := makeQueueDocument(t, app, other, models.DocStatusPending, "")

	deleted, err := discardDocument(app, document.Id, owner)
	if err != nil {
		t.Fatalf("discardDocument: %v", err)
	}
	if deleted {
		t.Fatal("the sweep crossed an ownership boundary")
	}
}

// The whole sweep must not fail over one row that is already gone.
func TestDiscardDocumentTreatsAGoneDocumentAsDone(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "discard-gone@example.test")

	deleted, err := discardDocument(app, "nonexistentid00", owner)
	if err != nil {
		t.Fatalf("discardDocument: %v", err)
	}
	if deleted {
		t.Fatal("reported deleting a document that does not exist")
	}
}
