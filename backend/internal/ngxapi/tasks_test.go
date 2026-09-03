package ngxapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase/core"
)

func createJob(t *testing.T, app core.App, documentID, status string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatalf("processing_jobs collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("document", documentID)
	record.Set("status", status)
	record.Set("task_id", uuid.NewSHA1(uuid.NameSpaceOID, []byte("job-"+documentID)).String())
	if err := app.Save(record); err != nil {
		t.Fatalf("save job for %s: %v", documentID, err)
	}
	return record.Id
}

func (f listFixture) tasks(t *testing.T) []map[string]any {
	t.Helper()
	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodGet, "/api/tasks/?task_name=consume_file", nil)
	e.Response = httptest.NewRecorder()

	user, err := f.app.FindRecordById("users", f.userID)
	if err != nil {
		t.Fatalf("load auth user: %v", err)
	}
	e.Auth = user

	if err := handleListTasks(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return body
}

// TestTaskListReadsItsDocumentsInOneQuery: swift-paperless polls this endpoint
// while an upload is in flight, and every task reports its document's file name
// and client id. Reading those per task made one poll a hundred round trips.
func TestTaskListReadsItsDocumentsInOneQuery(t *testing.T) {
	f := newListFixture(t)
	for _, docID := range []string{f.docBoth, f.docOne, f.docNone} {
		createJob(t, f.app, docID, "completed")
	}

	queries := countDocumentQueries(t, f.app)
	if got := len(f.tasks(t)); got != 3 {
		t.Fatalf("listed %d tasks, want 3", got)
	}
	// Two, whatever the page size: the owner filter joins documents to scope
	// the jobs, and the batch reads the columns the response needs. A third
	// would mean it went back to one lookup per task.
	if n := queries(); n != 2 {
		t.Fatalf("a page of 3 tasks ran %d document queries, want 2", n)
	}
}

// TestTaskReportsTheStoredDocumentID: related_document is an id the client
// turns around and fetches, so it has to be the stored one.
func TestTaskReportsTheStoredDocumentID(t *testing.T) {
	f := newListFixture(t)
	createJob(t, f.app, f.docOne, "completed")

	tasks := f.tasks(t)
	if len(tasks) != 1 {
		t.Fatalf("listed %d tasks, want 1", len(tasks))
	}
	want := storedID(t, f.app, "documents", f.docOne)
	if got := tasks[0]["related_document"]; got != fmt.Sprint(want) {
		t.Fatalf("related_document = %v, want %d", got, want)
	}
	if got := tasks[0]["result"]; got != fmt.Sprintf("Success. New document id %d created", want) {
		t.Fatalf("result = %v, want it to name document %d", got, want)
	}
}

// TestTaskSurvivesADeletedDocument: the job outlives the document it made, and
// a task list that panicked or reported an id nothing answers to would break
// the upload screen rather than the deleted document.
func TestTaskSurvivesADeletedDocument(t *testing.T) {
	f := newListFixture(t)
	createJob(t, f.app, f.docOne, "completed")

	record := mustFind(t, f.app, "documents", f.docOne)
	if err := f.app.Delete(record); err != nil {
		t.Fatalf("delete document: %v", err)
	}

	// The list filters on document.user, so a job whose document is gone drops
	// out entirely -- what matters is that it does so without failing.
	if got := len(f.tasks(t)); got != 0 {
		t.Fatalf("listed %d tasks, want the orphaned job to be filtered out", got)
	}
}

// TestTaskWireShapeDecodes is the bug that made swift-paperless show an empty
// "All tasks" while the server happily returned a hundred of them: the client
// types task_id as a UUID and parses the dates as ISO8601, and one field it
// cannot read fails the decode of the whole array -- silently, because a decode
// failure is not an HTTP error.
func TestTaskWireShapeDecodes(t *testing.T) {
	f := newListFixture(t)
	createJob(t, f.app, f.docOne, "running")

	tasks := f.tasks(t)
	if len(tasks) != 1 {
		t.Fatalf("listed %d tasks, want 1", len(tasks))
	}
	task := tasks[0]

	created, _ := task["date_created"].(string)
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", created); err != nil {
		t.Fatalf("date_created = %q, want ISO8601: %v", created, err)
	}
	if got := task["date_done"]; got != nil {
		t.Fatalf("date_done = %v for a running job, want null", got)
	}
	taskID, _ := task["task_id"].(string)
	if _, err := uuid.Parse(taskID); err != nil {
		t.Fatalf("task_id = %q, want a UUID: %v", taskID, err)
	}
}

// TestUnstartedJobStillHasATaskID: the worker stamps task_id when it picks a
// job up, so a job that has only been queued has none -- and an empty string is
// not a UUID the client will accept.
func TestUnstartedJobStillHasATaskID(t *testing.T) {
	f := newListFixture(t)
	jobID := createJob(t, f.app, f.docOne, "pending")

	job := mustFind(t, f.app, "processing_jobs", jobID)
	job.Set("task_id", "")
	if err := f.app.Save(job); err != nil {
		t.Fatalf("clear task_id: %v", err)
	}

	tasks := f.tasks(t)
	if len(tasks) != 1 {
		t.Fatalf("listed %d tasks, want 1", len(tasks))
	}
	taskID, _ := tasks[0]["task_id"].(string)
	if _, err := uuid.Parse(taskID); err != nil {
		t.Fatalf("task_id = %q, want a derived UUID: %v", taskID, err)
	}
	// Derived, not random: polling twice must not look like two tasks.
	if again, _ := f.tasks(t)[0]["task_id"].(string); again != taskID {
		t.Fatalf("task_id changed between polls: %q then %q", taskID, again)
	}
}

// TestFinishedJobReportsWhenItFinished
func TestFinishedJobReportsWhenItFinished(t *testing.T) {
	f := newListFixture(t)
	jobID := createJob(t, f.app, f.docOne, "completed")

	job := mustFind(t, f.app, "processing_jobs", jobID)
	job.Set("finished_at", "2026-03-04 05:06:07.000Z")
	if err := f.app.Save(job); err != nil {
		t.Fatalf("set finished_at: %v", err)
	}

	done, _ := f.tasks(t)[0]["date_done"].(string)
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", done); err != nil {
		t.Fatalf("date_done = %q, want ISO8601: %v", done, err)
	}
}

func (f listFixture) tasksFiltered(t *testing.T, query string) []map[string]any {
	t.Helper()
	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodGet, "/api/tasks/?"+query, nil)
	e.Response = httptest.NewRecorder()
	e.Auth = mustFind(t, f.app, "users", f.userID)

	if err := handleListTasks(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	return body
}

func (f listFixture) acknowledge(t *testing.T, authID string, ids []int) (int, map[string]any) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{"tasks": ids})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodPost, "/api/acknowledge_tasks/", bytes.NewReader(payload))
	e.Request.Header.Set("Content-Type", "application/json")
	e.Response = httptest.NewRecorder()
	e.Auth = mustFind(t, f.app, "users", authID)

	if err := handleAcknowledgeTasks(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	var body map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, body
}

// TestAcknowledgeDropsATaskFromThePoll is the whole point of the column: the
// client polls acknowledged=false and dismisses what it has shown, and without
// somewhere to record that, every finished upload came back on every poll.
func TestAcknowledgeDropsATaskFromThePoll(t *testing.T) {
	f := newListFixture(t)
	kept := createJob(t, f.app, f.docBoth, "completed")
	dismissed := createJob(t, f.app, f.docOne, "completed")

	if got := len(f.tasksFiltered(t, "acknowledged=false")); got != 2 {
		t.Fatalf("%d unacknowledged tasks before, want 2", got)
	}

	status, body := f.acknowledge(t, f.userID, []int{storedID(t, f.app, "processing_jobs", dismissed)})
	if status != http.StatusOK {
		t.Fatalf("acknowledge status %d", status)
	}
	if got := body["result"]; got != float64(1) {
		t.Fatalf("result = %v, want 1", got)
	}

	pending := f.tasksFiltered(t, "acknowledged=false")
	if len(pending) != 1 {
		t.Fatalf("%d unacknowledged tasks after, want 1", len(pending))
	}
	if got := int(pending[0]["id"].(float64)); got != storedID(t, f.app, "processing_jobs", kept) {
		t.Fatalf("the wrong task survived: %d", got)
	}

	done := f.tasksFiltered(t, "acknowledged=true")
	if len(done) != 1 || done[0]["acknowledged"] != true {
		t.Fatalf("acknowledged=true returned %v", done)
	}
}

// TestUnfilteredPollStillSeesEverything: absent is not false. A client that
// sends no acknowledged filter asks for the lot.
func TestUnfilteredPollStillSeesEverything(t *testing.T) {
	f := newListFixture(t)
	job := createJob(t, f.app, f.docOne, "completed")
	f.acknowledge(t, f.userID, []int{storedID(t, f.app, "processing_jobs", job)})

	if got := len(f.tasksFiltered(t, "")); got != 1 {
		t.Fatalf("an unfiltered poll returned %d tasks, want the acknowledged one too", got)
	}
}

// TestAcknowledgeStaysWithinTheOwner: a job belongs to an account only through
// its document, so the ownership check is a join rather than a column.
func TestAcknowledgeStaysWithinTheOwner(t *testing.T) {
	f := newListFixture(t)
	mine := createJob(t, f.app, f.docOne, "completed")

	status, body := f.acknowledge(t, f.otherID, []int{storedID(t, f.app, "processing_jobs", mine)})
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if got := body["result"]; got != float64(0) {
		t.Fatalf("another account acknowledged %v of my tasks", got)
	}
	if got := len(f.tasksFiltered(t, "acknowledged=false")); got != 1 {
		t.Fatalf("my task was dismissed by someone else")
	}
}

// TestAcknowledgeIgnoresUnknownIDs rather than failing the batch: the client
// sends the ids it last saw, and one already-deleted job must not stop the rest
// being dismissed.
func TestAcknowledgeIgnoresUnknownIDs(t *testing.T) {
	f := newListFixture(t)
	job := createJob(t, f.app, f.docOne, "completed")

	_, body := f.acknowledge(t, f.userID, []int{0, 999999999, storedID(t, f.app, "processing_jobs", job)})
	if got := body["result"]; got != float64(1) {
		t.Fatalf("result = %v, want the one real task", got)
	}
}
