package ngxapi

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

func mapDocument(app core.App, record *core.Record) map[string]any {
	created := record.GetString("created")
	updated := record.GetString("updated")
	docDate := record.GetString("document_date")
	if docDate == "" {
		docDate = createdDateOnly(created)
	}

	tagIDs := ngxTagIDs(app, record.GetStringSlice("tags"))
	if tagIDs == nil {
		tagIDs = []int{}
	}

	fileName := record.GetString("file")
	content := stripHTML(record.GetString("ocr_text"))
	createdFormatted := formatNgxCreatedDate(docDate)

	var owner any
	if uid := record.GetString("user"); uid != "" {
		owner = toNgxID(uid)
	}

	return map[string]any{
		"id":                    toNgxID(record.Id),
		"title":                 record.GetString("title"),
		"content":               content,
		"tags":                  tagIDs,
		"document_type":         relationID(record, "document_type"),
		"correspondent":         relationID(record, "correspondent"),
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
		"id":                 toNgxID(record.Id),
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
		"id":                 toNgxID(record.Id),
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
		"id":                 toNgxID(record.Id),
		"name":               name,
		"slug":               slugify(name),
		"match":              "",
		"matching_algorithm": 1,
		"is_insensitive":     true,
	}
}

func mapTask(app core.App, job *core.Record) map[string]any {
	status := mapJobStatus(job.GetString("status"))
	docID := job.GetString("document")
	result := taskResultMessage(app, job, status, docID)

	var relatedDoc any
	if status == "SUCCESS" && docID != "" {
		relatedDoc = strconv.Itoa(toNgxID(docID))
	}

	fileName := ""
	if docID != "" {
		if doc, err := app.FindRecordById("documents", docID); err == nil {
			fileName = doc.GetString("file")
		}
	}

	return map[string]any{
		"id":               toNgxID(job.Id),
		"task_id":          job.GetString("task_id"),
		"task_file_name":   fileName,
		"date_created":     job.GetString("created"),
		"date_done":        job.GetString("finished_at"),
		"type":             "file",
		"status":           status,
		"result":           result,
		"acknowledged":     false,
		"related_document": relatedDoc,
	}
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

func taskResultMessage(app core.App, job *core.Record, status, docID string) string {
	switch status {
	case "SUCCESS":
		ngxDocID := toNgxID(docID)
		return fmt.Sprintf("Success. New document id %d created", ngxDocID)
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

func createdDateOnly(datetime string) string {
	if datetime == "" {
		return ""
	}
	if t, err := time.Parse("2006-01-02 15:04:05.000Z", datetime); err == nil {
		return t.Format("2006-01-02")
	}
	if len(datetime) >= 10 {
		return datetime[:10]
	}
	return datetime
}

func documentSortField(ordering string) string {
	if ordering == "" {
		return "-created"
	}
	field := strings.TrimPrefix(strings.TrimPrefix(ordering, "-"), "+")
	desc := strings.HasPrefix(ordering, "-")

	var pbField string
	switch field {
	case "created", "created_date":
		pbField = "document_date"
	case "added":
		pbField = "created"
	case "modified":
		pbField = "updated"
	case "title":
		pbField = "title"
	default:
		pbField = "created"
	}

	if desc || strings.HasPrefix(ordering, "-") {
		return "-" + pbField
	}
	return pbField
}
