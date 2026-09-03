package ngxapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/pocketbase/pocketbase/core"
)

var htmlTagRE = regexp.MustCompile(`<[^>]*>`)

// singleRelations maps a document's single-relation fields to the collection
// each one points at, which is what the lens is keyed by.
var singleRelations = map[string]string{
	"document_type": "document_types",
	"correspondent": "correspondents",
}

// ngxIDLens holds the client-facing ids of everything a response refers to by
// PocketBase id.
//
// Client ids live in a column now rather than being derived from the record id,
// so naming a document's tag means reading the tag's row. One query per related
// collection per response, not one per field per row: a page of 250 documents
// would otherwise be 500 point lookups, which is the traffic shape the stored
// id exists to remove.
type ngxIDLens struct {
	byCollection map[string]map[string]int
}

func newNgxIDLens(app core.App, records []*core.Record) (*ngxIDLens, error) {
	wanted := map[string]map[string]struct{}{}
	add := func(collection, pbID string) {
		if pbID == "" {
			return
		}
		set := wanted[collection]
		if set == nil {
			set = map[string]struct{}{}
			wanted[collection] = set
		}
		set[pbID] = struct{}{}
	}

	for _, record := range records {
		for _, pbID := range record.GetStringSlice("tags") {
			add("tags", pbID)
		}
		for field, collection := range singleRelations {
			add(collection, record.GetString(field))
		}
	}

	lens := &ngxIDLens{byCollection: make(map[string]map[string]int, len(wanted))}
	for collection, set := range wanted {
		pbIDs := make([]string, 0, len(set))
		for pbID := range set {
			pbIDs = append(pbIDs, pbID)
		}
		known, err := ngxIDsByPBID(app, collection, pbIDs)
		if err != nil {
			return nil, err
		}
		lens.byCollection[collection] = known
	}
	return lens, nil
}

// id is 0 for a relation pointing at a row that is gone, or at one no create
// hook ever stamped. Both render as absent rather than as id 0, which is not an
// id this server issues.
func (l *ngxIDLens) id(collection, pbID string) int {
	if l == nil || pbID == "" {
		return 0
	}
	return l.byCollection[collection][pbID]
}

func (l *ngxIDLens) ids(collection string, pbIDs []string) []int {
	result := make([]int, 0, len(pbIDs))
	for _, pbID := range pbIDs {
		if id := l.id(collection, pbID); id > 0 {
			result = append(result, id)
		}
	}
	return result
}

func (l *ngxIDLens) relation(record *core.Record, field string) any {
	id := l.id(singleRelations[field], record.GetString(field))
	if id == 0 {
		return nil
	}
	return id
}

// mapDocument renders one document in paperless-ngx's shape. truncate is the
// client's truncate_content flag: list responses over a large archive are
// mostly OCR text, and a client that will only show a preview line says so.
func mapDocument(lens *ngxIDLens, record *core.Record, truncate bool) map[string]any {
	created := record.GetString("created")
	updated := record.GetString("updated")
	docDate := record.GetString("document_date")
	if docDate == "" {
		docDate = createdDateOnly(created)
	}

	tagIDs := lens.ids("tags", record.GetStringSlice("tags"))

	fileName := record.GetString("file")
	content := stripHTML(record.GetString("ocr_text"))
	if truncate {
		content = truncateContent(content)
	}
	createdFormatted := formatNgxCreatedDate(docDate)

	// Users carry no stored id: a client reads owner to compare it with the id
	// in /api/ui_settings/ and never sends it back, so both sides stay derived.
	var owner any
	if uid := record.GetString("user"); uid != "" {
		owner = toNgxID(uid)
	}

	return map[string]any{
		"id":                    ngxIDOf(record),
		"title":                 record.GetString("title"),
		"content":               content,
		"tags":                  tagIDs,
		"document_type":         lens.relation(record, "document_type"),
		"correspondent":         lens.relation(record, "correspondent"),
		"storage_path":          nil,
		"created":               createdFormatted,
		"created_date":          createdDateOnly(docDate),
		"added":                 formatNgxDateTime(created),
		"modified":              formatNgxDateTime(updated),
		"archive_serial_number": nil,
		"original_file_name":    fileName,
		"archived_file_name":    fileName,
		"checksum":              record.GetString("checksum"),
		"archive_checksum":      nil,
		"owner":                 owner,
		"user_can_change":       true,
		"notes":                 []any{},
		"custom_fields":         []any{},
	}
}

func mapTag(record *core.Record) map[string]any {
	name := record.GetString("name")
	return map[string]any{
		"id":                 ngxIDOf(record),
		"is_inbox_tag":       false,
		"name":               name,
		"slug":               slugify(name),
		"color":              "#a6cee3",
		"text_color":         "#000000",
		"match":              "",
		"matching_algorithm": 1,
		"is_insensitive":     true,
	}
}

func mapCorrespondent(record *core.Record) map[string]any {
	name := record.GetString("name")
	return map[string]any{
		"id":                 ngxIDOf(record),
		"name":               name,
		"slug":               slugify(name),
		"match":              "",
		"matching_algorithm": 1,
		"is_insensitive":     true,
	}
}

func mapDocumentType(record *core.Record) map[string]any {
	name := record.GetString("name")
	return map[string]any{
		"id":                 ngxIDOf(record),
		"name":               name,
		"slug":               slugify(name),
		"match":              "",
		"matching_algorithm": 1,
		"is_insensitive":     true,
	}
}

// mapTask renders one processing job as a paperless-ngx task. doc is the job's
// document, already read by the caller for the whole page, and nil when the job
// has none or it is gone.
func mapTask(job *core.Record, doc *taskDocument) map[string]any {
	status := mapJobStatus(job.GetString("status"))
	result := taskResultMessage(job, status, doc)

	var relatedDoc any
	if status == "SUCCESS" && doc != nil {
		relatedDoc = strconv.Itoa(doc.NgxID)
	}

	fileName := ""
	if doc != nil {
		fileName = doc.File
	}

	// Both date fields go through formatNgxDateTime. PocketBase stores a
	// datetime as "2006-01-02 15:04:05.000Z", and paperless clients parse
	// ISO8601 -- swift-paperless throws on the space, and a throw on one field
	// fails the decode of the whole task array, so the task list renders empty
	// with no error anywhere. date_done is null rather than "" while a job is
	// still running, for the same reason.
	var dateDone any
	if finished := formatNgxDateTime(job.GetString("finished_at")); finished != "" {
		dateDone = finished
	}

	return map[string]any{
		"id":             ngxIDOf(job),
		"task_id":        taskUUID(job),
		"task_file_name": fileName,
		// Clients request the list by task name and paperless reports it back.
		// Every Lemmary job is a file consumption, which is the only name a
		// paperless client asks for.
		"task_name":        "consume_file",
		"date_created":     formatNgxDateTime(job.GetString("created")),
		"date_done":        dateDone,
		"type":             "file",
		"status":           status,
		"result":           result,
		"acknowledged":     job.GetBool(ngxAcknowledgedField),
		"related_document": relatedDoc,
	}
}

// taskUUID is the job's Celery-style task id.
//
// Clients type this field as a UUID and will not accept anything else -- one
// value they cannot parse fails the decode of the entire task list, and a
// decode failure is not an HTTP error, so the client shows an empty task list
// and reports nothing.
//
// The worker stamps a real UUID when it picks a job up. Anything else -- an
// unstarted job, or a task_id some other ingest path wrote -- is answered with
// one derived from the job id, so it is stable across polls rather than looking
// like a new task each time.
func taskUUID(job *core.Record) string {
	stored := job.GetString("task_id")
	if _, err := uuid.Parse(stored); err == nil {
		return stored
	}
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(job.Id)).String()
}

func mapJobStatus(status string) string {
	switch status {
	case "completed", "needs_review":
		return "SUCCESS"
	case "failed":
		return "FAILURE"
	case "running":
		return "STARTED"
	default:
		return "PENDING"
	}
}

func taskResultMessage(job *core.Record, status string, doc *taskDocument) string {
	switch status {
	case "SUCCESS":
		if doc == nil {
			return "Success."
		}
		return fmt.Sprintf("Success. New document id %d created", doc.NgxID)
	case "FAILURE":
		msg := latestStepError(job)
		if msg == "" {
			msg = "Processing failed"
		}
		return msg
	case "STARTED":
		return "Processing document"
	default:
		return "Waiting for consumption"
	}
}

type mappedStepRun struct {
	Name  string `json:"name"`
	Error string `json:"error"`
}

func latestStepError(job *core.Record) string {
	raw := job.Get("step_runs")
	if raw == nil {
		return ""
	}

	data, err := json.Marshal(raw)
	if err != nil {
		return ""
	}

	var runs []mappedStepRun
	if err := json.Unmarshal(data, &runs); err != nil {
		return ""
	}
	for i := len(runs) - 1; i >= 0; i-- {
		if msg := strings.TrimSpace(runs[i].Error); msg != "" {
			if runs[i].Name != "" {
				return fmt.Sprintf("%s: %s", runs[i].Name, msg)
			}
			return msg
		}
	}
	return ""
}

// truncateContent cuts on a rune boundary, so a multi-byte character straddling
// the limit does not reach the client as a replacement character.
func truncateContent(s string) string {
	if len(s) <= truncatedContentLen {
		return s
	}
	runes := []rune(s)
	if len(runes) <= truncatedContentLen {
		return s
	}
	return string(runes[:truncatedContentLen])
}

func stripHTML(s string) string {
	return strings.TrimSpace(htmlTagRE.ReplaceAllString(s, ""))
}

func slugify(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = regexp.MustCompile(`[^a-z0-9-]`).ReplaceAllString(s, "")
	return s
}
