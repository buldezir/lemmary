package ngxapi

import (
	"errors"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/fulltext"
	"paperless-go/backend/internal/models"
)

var errSearchIndexNotReady = errors.New("search index is not ready")

func handleListDocuments(idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		page, pageSize := paginationParams(e)
		authID := e.Auth.Id

		query := strings.TrimSpace(e.Request.URL.Query().Get("query"))
		if query != "" {
			return listDocumentsFulltext(e, idx, authID, query, page, pageSize)
		}

		filter, params := ownerScope(authID)

		total, err := e.App.CountRecords("documents", dbx.NewExp(filter, params))
		if err != nil {
			return internalError(e, err)
		}

		sort := documentSortField(e.Request.URL.Query().Get("ordering"))
		offset := (page - 1) * pageSize

		records, err := e.App.FindRecordsByFilter(
			"documents",
			filter,
			sort,
			pageSize,
			offset,
			params,
		)
		if err != nil {
			return internalError(e, err)
		}

		results := make([]any, 0, len(records))
		for _, record := range records {
			results = append(results, mapDocument(e.App, record))
		}

		return paginatedList(e, total, page, pageSize, results)
	}
}

func listDocumentsFulltext(e *core.RequestEvent, idx *fulltext.Index, authID, query string, page, pageSize int) error {
	if idx == nil || !idx.Ready() {
		return internalError(e, errSearchIndexNotReady)
	}

	result, err := idx.Search(fulltext.Query{
		Text:   query,
		UserID: authID,
		Offset: (page - 1) * pageSize,
		Limit:  pageSize,
	})
	if err != nil {
		return internalError(e, err)
	}

	results := make([]any, 0, len(result.Hits))
	for _, hit := range result.Hits {
		record, err := e.App.FindRecordById("documents", hit.ID)
		if err != nil {
			continue
		}
		if authID != "" && record.GetString("user") != authID {
			continue
		}
		results = append(results, mapDocument(e.App, record))
	}

	return paginatedList(e, int64(result.Total), page, pageSize, results)
}

func handleGetDocument(e *core.RequestEvent) error {
	record, err := findOwnedDocument(e.App, e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	return writeJSON(e, http.StatusOK, mapDocument(e.App, record))
}

func handlePatchDocument(e *core.RequestEvent) error {
	record, err := findOwnedDocument(e.App, e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}

	var body map[string]any
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}

	if v, ok := body["title"].(string); ok {
		record.Set("title", v)
	}
	if v, ok := body["content"].(string); ok {
		record.Set("ocr_text", v)
	}
	if v, ok := body["created"].(string); ok {
		record.Set("document_date", v)
	}
	if v, ok := body["document_type"]; ok {
		setRelationField(e.App, record, "document_type", v, e.Auth.Id)
	}
	if v, ok := body["correspondent"]; ok {
		setRelationField(e.App, record, "correspondent", v, e.Auth.Id)
	}
	if v, ok := body["tags"].([]any); ok {
		raw := make([]string, 0, len(v))
		for _, item := range v {
			switch tagID := item.(type) {
			case float64:
				raw = append(raw, strconv.Itoa(int(tagID)))
			case int:
				raw = append(raw, strconv.Itoa(tagID))
			case string:
				raw = append(raw, tagID)
			}
		}
		record.Set("tags", resolveTagPBIDs(e.App, raw))
	}

	record.Set("metadata_source", models.MetadataSourceUser)
	if err := e.App.Save(record); err != nil {
		return badRequest(e, err.Error())
	}

	return writeJSON(e, http.StatusOK, mapDocument(e.App, record))
}

func handleDeleteDocument(e *core.RequestEvent) error {
	record, err := findOwnedDocument(e.App, e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	if err := e.App.Delete(record); err != nil {
		return internalError(e, err)
	}
	e.Response.WriteHeader(http.StatusNoContent)
	return nil
}

func handlePostDocument(e *core.RequestEvent) error {
	files, err := e.FindUploadedFiles("document")
	if err != nil {
		return badRequest(e, "Missing document file.")
	}

	if err := e.Request.ParseMultipartForm(32 << 20); err != nil {
		return badRequest(e, "Invalid multipart form.")
	}

	collection, err := e.App.FindCollectionByNameOrId("documents")
	if err != nil {
		return internalError(e, err)
	}

	record := core.NewRecord(collection)
	record.Set("user", e.Auth.Id)
	record.Set("file", files[0])
	record.Set("processing_status", models.DocStatusPending)

	form := e.Request.MultipartForm
	if form != nil {
		if title := firstFormValue(form, "title"); title != "" {
			record.Set("title", title)
		}
		if created := firstFormValue(form, "created"); created != "" {
			record.Set("document_date", createdDateOnly(created))
		}
		if correspondent := firstFormValue(form, "correspondent"); correspondent != "" {
			if pbID := resolvePBRelationID(e.App, "correspondents", correspondent, e.Auth.Id); pbID != "" {
				record.Set("correspondent", pbID)
			}
		}
		if docType := firstFormValue(form, "document_type"); docType != "" {
			if pbID := resolvePBRelationID(e.App, "document_types", docType, e.Auth.Id); pbID != "" {
				record.Set("document_type", pbID)
			}
		}
		if tagIDs := parseTagIDs(form.Value); len(tagIDs) > 0 {
			record.Set("tags", resolveTagPBIDs(e.App, tagIDs))
		}
	}

	if err := e.App.Save(record); err != nil {
		return badRequest(e, err.Error())
	}

	taskID, err := findTaskIDForDocument(e.App, record.Id)
	if err != nil {
		return internalError(e, err)
	}

	return writeJSON(e, http.StatusOK, taskID)
}

func handleDownloadDocument(e *core.RequestEvent) error {
	record, err := findOwnedDocument(e.App, e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}

	fileName := record.GetString("file")
	if fileName == "" {
		return notFound(e, "Document has no file.")
	}

	fsys, err := e.App.NewFilesystem()
	if err != nil {
		return internalError(e, err)
	}
	defer fsys.Close()

	fileKey := record.BaseFilesPath() + "/" + fileName
	return fsys.Serve(e.Response, e.Request, fileKey, fileName)
}

func handleDocumentThumb(e *core.RequestEvent) error {
	record, err := findOwnedDocument(e.App, e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}

	fileName := record.GetString("preview")
	if fileName == "" {
		return notFound(e, "Document has no preview.")
	}

	fsys, err := e.App.NewFilesystem()
	if err != nil {
		return internalError(e, err)
	}
	defer fsys.Close()

	fileKey := record.BaseFilesPath() + "/" + fileName
	return fsys.Serve(e.Response, e.Request, fileKey, fileName)
}

func findTaskIDForDocument(app core.App, documentID string) (string, error) {
	jobs, err := app.FindRecordsByFilter(
		"processing_jobs",
		"document = {:docId}",
		"-created",
		1,
		0,
		map[string]any{"docId": documentID},
	)
	if err != nil {
		return "", err
	}
	if len(jobs) == 0 {
		return "", errors.New("processing job not found")
	}
	taskID := jobs[0].GetString("task_id")
	if taskID == "" {
		return jobs[0].Id, nil
	}
	return taskID, nil
}

func setRelationField(app core.App, record *core.Record, field string, value any, authID string) {
	if value == nil {
		record.Set(field, "")
		return
	}
	pbID := resolvePBRelationID(app, collectionForRelationField(field), value, authID)
	record.Set(field, pbID)
}

func collectionForRelationField(field string) string {
	switch field {
	case "correspondent":
		return "correspondents"
	case "document_type":
		return "document_types"
	default:
		return ""
	}
}

func firstFormValue(form *multipart.Form, key string) string {
	if form == nil {
		return ""
	}
	values := form.Value[key]
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}

func parseTagIDs(form url.Values) []string {
	var ids []string
	for _, v := range form["tags"] {
		if strings.TrimSpace(v) != "" {
			ids = append(ids, v)
		}
	}
	return ids
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
