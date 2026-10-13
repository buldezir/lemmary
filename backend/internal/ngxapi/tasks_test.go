package ngxapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
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
	e.Request.Header.Set("Accept", "application/json; version=9")
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

// swift-paperless polls this endpoint while an upload is in flight; reading the
// document per task made one poll a hundred round trips.
func TestTaskListReadsItsDocumentsInOneQuery(t *testing.T) {
	f := newListFixture(t)
	for _, docID := range []string{f.docBoth, f.docOne, f.docNone} {
		createJob(t, f.app, docID, "completed")
	}

	queries := countDocumentQueries(t, f.app)
	if got := len(f.tasks(t)); got != 3 {
		t.Fatalf("listed %d tasks, want 3", got)
	}
	// One, whatever the page size: the batch reads the columns the response
	// needs. The owner filter is a subquery inside the job query, which the
	// counter does not see. A second would mean one lookup per task.
	if n := queries(); n != 1 {
		t.Fatalf("a page of 3 tasks ran %d document queries, want 1", n)
	}
}

// related_document is an id the client turns around and fetches.
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

// The job outlives the document it made.
func TestTaskSurvivesADeletedDocument(t *testing.T) {
	f := newListFixture(t)
	createJob(t, f.app, f.docOne, "completed")

	record := mustFind(t, f.app, "documents", f.docOne)
	if err := f.app.Delete(record); err != nil {
		t.Fatalf("delete document: %v", err)
	}

	// The list filters on document.user, so a job whose document is gone drops
	// out entirely; what matters is that it does so without failing.
	if got := len(f.tasks(t)); got != 0 {
		t.Fatalf("listed %d tasks, want the orphaned job to be filtered out", got)
	}
}

// The client types task_id as a UUID and parses the dates as ISO8601, and one
// field it cannot read fails the decode of the whole array, silently, because a
// decode failure is not an HTTP error.
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

// The worker stamps task_id when it picks a job up, and an empty string is not a
// UUID the client will accept.
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
	e.Request.Header.Set("Accept", "application/json; version=9")
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

// The client polls acknowledged=false and dismisses what it has shown; without
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

// Absent is not false: a client that sends no acknowledged filter asks for the lot.
func TestUnfilteredPollStillSeesEverything(t *testing.T) {
	f := newListFixture(t)
	job := createJob(t, f.app, f.docOne, "completed")
	f.acknowledge(t, f.userID, []int{storedID(t, f.app, "processing_jobs", job)})

	if got := len(f.tasksFiltered(t, "")); got != 1 {
		t.Fatalf("an unfiltered poll returned %d tasks, want the acknowledged one too", got)
	}
}

// A job belongs to an account only through its document, so the ownership check
// is a join rather than a column.
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

// The client sends the ids it last saw, and one already-deleted job must not
// stop the rest being dismissed.
func TestAcknowledgeIgnoresUnknownIDs(t *testing.T) {
	f := newListFixture(t)
	job := createJob(t, f.app, f.docOne, "completed")

	_, body := f.acknowledge(t, f.userID, []int{0, 999999999, storedID(t, f.app, "processing_jobs", job)})
	if got := body["result"]; got != float64(1) {
		t.Fatalf("result = %v, want the one real task", got)
	}
}

func (f listFixture) tasksV10(t *testing.T, path string) (int, []byte) {
	t.Helper()
	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodGet, path, nil)
	e.Response = httptest.NewRecorder()
	e.Auth = mustFind(t, f.app, "users", f.userID)

	handler := handleListTasks
	switch {
	case strings.HasPrefix(path, "/api/tasks/active/"):
		handler = handleActiveTasks
	case strings.HasPrefix(path, "/api/tasks/status_counts/"):
		handler = handleTaskStatusCounts
	}
	if err := handler(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	rec := e.Response.(*httptest.ResponseRecorder)
	return rec.Code, rec.Body.Bytes()
}

func (f listFixture) taskPage(t *testing.T, query string) paginatedResponse {
	t.Helper()
	status, raw := f.tasksV10(t, "/api/tasks/?"+query)
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	var page paginatedResponse
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return page
}

// paperless-ngx 3 assumes v10 when a request names no version, and v10 pages
// the task list and renames its fields.
func TestTaskListV10IsAPageWithTheNewFieldNames(t *testing.T) {
	f := newListFixture(t)
	createJob(t, f.app, f.docBoth, "completed")
	createJob(t, f.app, f.docNone, "completed")
	createJob(t, f.app, f.docOne, "completed")

	page := f.taskPage(t, "page_size=2")
	if page.Count != 3 || len(page.Results) != 2 || page.Next == nil {
		t.Fatalf("page = count %d, %d results, next %v; want 3, 2, a next link", page.Count, len(page.Results), page.Next)
	}

	want := float64(storedID(t, f.app, "documents", f.docBoth))
	var task map[string]any
	for _, result := range f.taskPage(t, "task_type=consume_file").Results {
		if r := result.(map[string]any); r["input_data"].(map[string]any)["filename"] == mustFind(t, f.app, "documents", f.docBoth).GetString("file") {
			task = r
		}
	}
	if task["task_type"] != "consume_file" || task["status"] != "success" {
		t.Fatalf("task = %v, want a successful consume_file", task)
	}
	if ids, _ := task["related_document_ids"].([]any); len(ids) != 1 || ids[0] != want {
		t.Fatalf("related_document_ids = %v, want [%v]", task["related_document_ids"], want)
	}
	if got := task["result_data"].(map[string]any)["document_id"]; got != want {
		t.Fatalf("result_data.document_id = %v, want %v", got, want)
	}
	if _, legacy := task["task_name"]; legacy {
		t.Fatal("v10 still carries the v9 task_name")
	}
}

func TestTaskListV10FiltersByStatus(t *testing.T) {
	f := newListFixture(t)
	createJob(t, f.app, f.docBoth, "needs_review")
	failed := createJob(t, f.app, f.docOne, "failed")
	running := createJob(t, f.app, f.docNone, "running")

	cases := map[string][]string{
		"status=failure":                      {failed},
		"status=failure&status=started":       {failed, running},
		"is_complete=false":                   {running},
		"status=success&task_type=mail_fetch": nil,
	}
	for query, want := range cases {
		page := f.taskPage(t, query)
		if int(page.Count) != len(want) {
			t.Fatalf("%s: %d tasks, want %d", query, page.Count, len(want))
		}
		for _, result := range page.Results {
			id := int(result.(map[string]any)["id"].(float64))
			if !slices.ContainsFunc(want, func(job string) bool { return storedID(t, f.app, "processing_jobs", job) == id }) {
				t.Fatalf("%s returned task %d", query, id)
			}
		}
	}

	if status, raw := f.tasksV10(t, "/api/tasks/?status=done"); status != http.StatusBadRequest {
		t.Fatalf("an unknown status answered %d, want 400: %s", status, raw)
	}
}

func TestActiveTasksAndStatusCounts(t *testing.T) {
	f := newListFixture(t)
	for _, status := range []string{"pending", "running", "completed", "needs_review", "failed", "cancelled"} {
		createJob(t, f.app, f.docOne, status)
	}

	status, raw := f.tasksV10(t, "/api/tasks/active/")
	var active []map[string]any
	if err := json.Unmarshal(raw, &active); status != http.StatusOK || err != nil {
		t.Fatalf("active: status %d, %v: %s", status, err, raw)
	}
	if len(active) != 2 {
		t.Fatalf("%d active tasks, want the pending and the running one", len(active))
	}

	// status is dropped, as paperless does: the counts are by status.
	status, raw = f.tasksV10(t, "/api/tasks/status_counts/?status=failure")
	var counts map[string]int
	if err := json.Unmarshal(raw, &counts); status != http.StatusOK || err != nil {
		t.Fatalf("status_counts: status %d, %v: %s", status, err, raw)
	}
	want := map[string]int{"all": 6, "needs_attention": 2, "in_progress": 2, "completed": 2}
	if !maps.Equal(counts, want) {
		t.Fatalf("counts = %v, want %v", counts, want)
	}
}

func TestAcknowledgeAllStaysWithinTheOwner(t *testing.T) {
	f := newListFixture(t)
	createJob(t, f.app, f.docOne, "completed")
	createJob(t, f.app, f.docNone, "failed")

	payload := bytes.NewReader([]byte(`{"all": true}`))
	e := &core.RequestEvent{}
	e.App = f.app
	e.Request = httptest.NewRequest(http.MethodPost, "/api/tasks/acknowledge/", payload)
	e.Request.Header.Set("Content-Type", "application/json")
	e.Response = httptest.NewRecorder()
	e.Auth = mustFind(t, f.app, "users", f.otherID)
	if err := handleAcknowledgeTasks(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := f.taskPage(t, "acknowledged=false").Count; got != 2 {
		t.Fatalf("another account's all:true dismissed my tasks: %d left", got)
	}

	e.Request = httptest.NewRequest(http.MethodPost, "/api/tasks/acknowledge/", bytes.NewReader([]byte(`{"all": true}`)))
	e.Request.Header.Set("Content-Type", "application/json")
	e.Response = httptest.NewRecorder()
	e.Auth = mustFind(t, f.app, "users", f.userID)
	if err := handleAcknowledgeTasks(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if got := f.taskPage(t, "acknowledged=false").Count; got != 0 {
		t.Fatalf("%d tasks left after all:true, want none", got)
	}
}
