package ngxapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ngxid"
)

// ngxAcknowledgedField records that a paperless client has dismissed a task.
// Nothing outside this API reads or writes it -- Lemmary shows a document's
// processing state on the document itself and has no notion of dismissing it.
const ngxAcknowledgedField = "ngx_acknowledged"

func handleListTasks(e *core.RequestEvent) error {
	query := e.Request.URL.Query()

	filter := "document.user = {:userId}"
	_, params := ownerScope(e.Auth.Id)

	if taskID := strings.TrimSpace(query.Get("task_id")); taskID != "" {
		filter += " && task_id = {:taskId}"
		params["taskId"] = taskID
	}

	// Clients poll with acknowledged=false and dismiss what they have shown.
	// Ignoring it meant every finished upload came back on every poll, forever.
	acknowledged, err := boolParam(query, "acknowledged")
	if err != nil {
		return badRequest(e, err.Error())
	}
	if acknowledged != nil {
		filter += " && " + ngxAcknowledgedField + " = {:acknowledged}"
		params["acknowledged"] = *acknowledged
	}

	records, err := e.App.FindRecordsByFilter(
		"processing_jobs",
		filter,
		"-created",
		100,
		0,
		params,
	)
	if err != nil {
		return internalError(e, err)
	}

	documents, err := taskDocuments(e.App, records)
	if err != nil {
		return internalError(e, err)
	}

	results := make([]any, 0, len(records))
	for _, job := range records {
		results = append(results, mapTask(job, documents[job.GetString("document")]))
	}

	return writeJSON(e, http.StatusOK, results)
}

// taskDocument is the slice of a document a task response needs: the file name
// it reports and the id a client can address the document by.
type taskDocument struct {
	ID    string `db:"id"`
	File  string `db:"file"`
	NgxID int    `db:"ngx_id"`
}

// taskDocuments reads them for a whole page of jobs in one query.
//
// Two reasons it is not a FindRecordById per job. It was one, and swift-
// paperless polls this endpoint while an upload is in flight, so a hundred jobs
// meant a hundred round trips per poll. And documents store their OCR text
// inline, so hydrating whole records would pull the text of a hundred documents
// to print two fields.
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
	}
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}

	ids := make([]any, 0, len(body.Tasks))
	for _, id := range body.Tasks {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return writeJSON(e, http.StatusOK, map[string]any{"result": 0})
	}

	// One statement, with ownership as a subquery rather than a prior lookup: a
	// job belongs to an account only through its document, and resolving the
	// records first would hydrate their step JSON to write one boolean.
	//
	// Raw SQL rather than a record save because this column belongs to this API
	// alone -- a save would fire the job hooks and move `updated`, which the
	// pipeline reads.
	result, err := e.App.DB().Update(
		"processing_jobs",
		dbx.Params{ngxAcknowledgedField: true},
		dbx.And(
			dbx.NewExp("[["+ngxid.Field+"]] > 0"),
			dbx.In("[["+ngxid.Field+"]]", ids...),
			dbx.NewExp(
				"[[document]] IN (SELECT [[documents.id]] FROM {{documents}} WHERE [[documents.user]] = {:userID})",
				dbx.Params{"userID": e.Auth.Id},
			),
		),
	).Execute()
	if err != nil {
		return internalError(e, err)
	}
	affected, _ := result.RowsAffected()

	return writeJSON(e, http.StatusOK, map[string]any{"result": affected})
}
