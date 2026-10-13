package ngxapi

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/ngxid"
)

// ngxAcknowledgedField records that a paperless client has dismissed a task.
// Nothing outside this API reads or writes it: Lemmary shows processing state
// on the document and has no notion of dismissing it.
const ngxAcknowledgedField = "ngx_acknowledged"

func handleListTasks(e *core.RequestEvent) error {
	query := e.Request.URL.Query()
	v9 := apiVersion(e) < 10
	scope, err := taskScope(e.Auth.Id, query, v9)
	if err != nil {
		return badRequest(e, err.Error())
	}

	if v9 {
		jobs, err := findTasks(e.App, scope, "-created", 100, 0)
		if err != nil {
			return internalError(e, err)
		}
		results, err := taskResults(e, jobs)
		if err != nil {
			return internalError(e, err)
		}
		return writeJSON(e, http.StatusOK, results)
	}

	page, pageSize := paginationParams(e)
	total, err := e.App.CountRecords("processing_jobs", scope...)
	if err != nil {
		return internalError(e, err)
	}
	jobs, err := findTasks(e.App, scope, taskOrdering(query.Get("ordering")), pageSize, (page-1)*pageSize)
	if err != nil {
		return internalError(e, err)
	}
	results, err := taskResults(e, jobs)
	if err != nil {
		return internalError(e, err)
	}
	return paginatedList(e, total, page, pageSize, results)
}

// handleActiveTasks is paperless-ngx 3's list of queued and running tasks.
func handleActiveTasks(e *core.RequestEvent) error {
	scope := []dbx.Expression{
		taskOwnerExp(e.Auth.Id),
		dbx.In("status", models.JobStatusPending, models.JobStatusRunning),
	}
	jobs, err := findTasks(e.App, scope, "-created", 50, 0)
	if err != nil {
		return internalError(e, err)
	}
	results, err := taskResults(e, jobs)
	if err != nil {
		return internalError(e, err)
	}
	return writeJSON(e, http.StatusOK, results)
}

// handleTaskStatusCounts answers paperless-ngx 3's task overview. Like
// paperless, it applies the list filters except the status ones it counts by.
func handleTaskStatusCounts(e *core.RequestEvent) error {
	query := e.Request.URL.Query()
	query.Del("status")
	query.Del("is_complete")
	scope, err := taskScope(e.Auth.Id, query, false)
	if err != nil {
		return badRequest(e, err.Error())
	}

	buckets := map[string][]string{
		"all":             nil,
		"needs_attention": {models.JobStatusFailed, models.JobStatusCancelled},
		"in_progress":     {models.JobStatusPending, models.JobStatusRunning},
		"completed":       {models.JobStatusCompleted, models.JobStatusNeedsReview},
	}
	counts := make(map[string]int64, len(buckets))
	for name, statuses := range buckets {
		exprs := slices.Clone(scope)
		if statuses != nil {
			exprs = append(exprs, dbx.In("status", anySlice(statuses)...))
		}
		n, err := e.App.CountRecords("processing_jobs", exprs...)
		if err != nil {
			return internalError(e, err)
		}
		counts[name] = n
	}
	return writeJSON(e, http.StatusOK, counts)
}

// A job belongs to an account only through its document.
func taskOwnerExp(authID string) dbx.Expression {
	return dbx.NewExp(
		"[[document]] IN (SELECT [[documents.id]] FROM {{documents}} WHERE [[documents.user]] = {:taskOwner})",
		dbx.Params{"taskOwner": authID},
	)
}

var taskStatuses = map[string][]string{
	"pending": {models.JobStatusPending},
	"started": {models.JobStatusRunning},
	"success": {models.JobStatusCompleted, models.JobStatusNeedsReview},
	"failure": {models.JobStatusFailed},
	"revoked": {models.JobStatusCancelled},
}

// taskScope reads the task list filters. v9 knew only task_id and
// acknowledged; paperless-ngx 3 filters by status and type as well.
func taskScope(authID string, query url.Values, v9 bool) ([]dbx.Expression, error) {
	scope := []dbx.Expression{taskOwnerExp(authID)}

	if taskID := strings.TrimSpace(query.Get("task_id")); taskID != "" {
		scope = append(scope, dbx.HashExp{"task_id": taskID})
	}

	// Clients poll with acknowledged=false and dismiss what they have shown.
	// Ignoring it meant every finished upload came back on every poll, forever.
	acknowledged, err := boolParam(query, "acknowledged")
	if err != nil {
		return nil, err
	}
	if acknowledged != nil {
		scope = append(scope, dbx.HashExp{ngxAcknowledgedField: *acknowledged})
	}
	if v9 {
		return scope, nil
	}

	if names := query["status"]; len(names) > 0 {
		var statuses []any
		for _, name := range names {
			mapped, ok := taskStatuses[name]
			if !ok {
				return nil, fmt.Errorf("Select a valid choice. %s is not one of the available choices.", name)
			}
			statuses = append(statuses, anySlice(mapped)...)
		}
		scope = append(scope, dbx.In("status", statuses...))
	}

	complete, err := boolParam(query, "is_complete")
	if err != nil {
		return nil, err
	}
	if complete != nil {
		done := dbx.In("status", models.JobStatusCompleted, models.JobStatusNeedsReview,
			models.JobStatusFailed, models.JobStatusCancelled)
		if *complete {
			scope = append(scope, done)
		} else {
			scope = append(scope, dbx.Not(done))
		}
	}

	if types := query["task_type"]; len(types) > 0 && !slices.Contains(types, ngxTaskType) {
		scope = append(scope, dbx.NewExp("1 = 0"))
	}
	return scope, nil
}

func taskOrdering(ordering string) string {
	column, ok := map[string]string{"date_created": "created", "date_done": "finished_at"}[strings.TrimPrefix(ordering, "-")]
	if !ok {
		return "-created"
	}
	if strings.HasPrefix(ordering, "-") {
		return "-" + column
	}
	return column
}

func findTasks(app core.App, scope []dbx.Expression, sort string, limit, offset int) ([]*core.Record, error) {
	order := "[[processing_jobs." + strings.TrimPrefix(sort, "-") + "]]"
	if strings.HasPrefix(sort, "-") {
		order += " DESC"
	}
	jobs := []*core.Record{}
	err := app.RecordQuery("processing_jobs").
		AndWhere(dbx.And(scope...)).
		OrderBy(order, "[[processing_jobs.id]]").
		Limit(int64(limit)).
		Offset(int64(offset)).
		All(&jobs)
	return jobs, err
}

func taskResults(e *core.RequestEvent, jobs []*core.Record) ([]any, error) {
	documents, err := taskDocuments(e.App, jobs)
	if err != nil {
		return nil, err
	}
	v9 := apiVersion(e) < 10
	owner := toNgxID(e.Auth.Id)
	results := make([]any, 0, len(jobs))
	for _, job := range jobs {
		doc := documents[job.GetString("document")]
		if v9 {
			results = append(results, mapTask(job, doc))
		} else {
			results = append(results, mapTaskV10(job, doc, owner))
		}
	}
	return results, nil
}

func anySlice(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

type taskDocument struct {
	ID    string `db:"id"`
	File  string `db:"file"`
	NgxID int    `db:"ngx_id"`
}

// taskDocuments reads them for a whole page of jobs in one query. swift-
// paperless polls this endpoint while an upload is in flight, and documents
// store their OCR text inline, so a FindRecordById per job pulled the text of a
// hundred documents to print two fields.
func taskDocuments(app core.App, jobs []*core.Record) (map[string]*taskDocument, error) {
	ids := make([]any, 0, len(jobs))
	seen := map[string]struct{}{}
	for _, job := range jobs {
		id := job.GetString("document")
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return map[string]*taskDocument{}, nil
	}

	var rows []taskDocument
	err := app.RecordQuery("documents").
		Select("[[documents.id]]", "[[documents.file]]", "[[documents.ngx_id]]").
		AndWhere(dbx.In("[[documents.id]]", ids...)).
		All(&rows)
	if err != nil {
		return nil, err
	}

	byID := make(map[string]*taskDocument, len(rows))
	for i := range rows {
		byID[rows[i].ID] = &rows[i]
	}
	return byID, nil
}

// handleAcknowledgeTasks marks tasks dismissed. Both paths are registered:
// paperless-ngx moved this to /api/tasks/acknowledge/ in 2.14, and clients pick
// one from the version the server advertises rather than trying both.
func handleAcknowledgeTasks(e *core.RequestEvent) error {
	var body struct {
		Tasks []int `json:"tasks"`
		All   bool  `json:"all"`
	}
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}

	// paperless-ngx 3 dismisses everything outstanding with {"all": true}.
	var target dbx.Expression = dbx.HashExp{ngxAcknowledgedField: false}
	if !body.All {
		ids := make([]any, 0, len(body.Tasks))
		for _, id := range body.Tasks {
			if id > 0 {
				ids = append(ids, id)
			}
		}
		if len(ids) == 0 {
			return writeJSON(e, http.StatusOK, map[string]any{"result": 0})
		}
		target = dbx.And(dbx.NewExp("[["+ngxid.Field+"]] > 0"), dbx.In("[["+ngxid.Field+"]]", ids...))
	}

	// One statement, with ownership as a subquery: a job belongs to an account
	// only through its document, and resolving the records first would hydrate
	// their step JSON to write one boolean. Raw SQL rather than a record save
	// because a save would fire the job hooks and move `updated`, which the
	// pipeline reads.
	result, err := e.App.DB().Update(
		"processing_jobs",
		dbx.Params{ngxAcknowledgedField: true},
		dbx.And(target, taskOwnerExp(e.Auth.Id)),
	).Execute()
	if err != nil {
		return internalError(e, err)
	}
	affected, _ := result.RowsAffected()

	return writeJSON(e, http.StatusOK, map[string]any{"result": affected})
}
