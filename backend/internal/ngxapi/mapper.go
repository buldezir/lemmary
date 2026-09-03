package ngxapi

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

var htmlTagRE = regexp.MustCompile(`<[^>]*>`)

// mapDocument renders one document in paperless-ngx's shape. truncate is the
// client's truncate_content flag: list responses over a large archive are
// mostly OCR text, and a client that will only show a preview line says so.
func mapDocument(app core.App, record *core.Record, truncate bool) map[string]any {
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
	if truncate {
		content = truncateContent(content)
	}
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
		"document_type":         ngxRelationID(record, "document_type"),
		"correspondent":         ngxRelationID(record, "correspondent"),
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
