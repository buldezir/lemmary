package ngxapi

import (
	"errors"
	"fmt"
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
			return listDocumentsFulltext(e, idx, authID, query, page, clampSearchPageSize(pageSize))
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
		record.Set("document_date", createdDateOnly(v))
	}
	if v, ok := body["document_type"]; ok {
		if err := setRelationField(e.App, record, "document_type", v, e.Auth.Id); err != nil {
			return badRequest(e, err.Error())
		}
	}
	if v, ok := body["correspondent"]; ok {
		if err := setRelationField(e.App, record, "correspondent", v, e.Auth.Id); err != nil {
			return badRequest(e, err.Error())
		}
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
		tagIDs, err := resolveTagPBIDs(e.App, raw, e.Auth.Id)
		if err != nil {
			return badRequest(e, err.Error())
		}
		record.Set("tags", tagIDs)
	}

	record.Set("metadata_source", models.MetadataSourceUser)
	if err := e.App.Save(record); err != nil {
		return saveError(e, err)
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
	// FindUploadedFiles already parsed the multipart form.
	files, err := e.FindUploadedFiles("document")
	if err != nil {
		return badRequest(e, "Missing document file.")
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
		if rawTagIDs := parseTagIDs(form.Value); len(rawTagIDs) > 0 {
			tagIDs, err := resolveTagPBIDs(e.App, rawTagIDs, e.Auth.Id)
			if err != nil {
				return badRequest(e, err.Error())
			}
			record.Set("tags", tagIDs)
		}
	}

	if err := e.App.Save(record); err != nil {
		return saveError(e, err)
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

// setRelationField resolves a client-sent relation id and errors when it does
// not resolve: silently storing "" would wipe the document's existing relation
// while the client sees a 200. nil, 0, and "" are explicit clears.
func setRelationField(app core.App, record *core.Record, field string, value any, authID string) error {
	switch v := value.(type) {
	case nil:
		record.Set(field, "")
		return nil
	case float64:
		if v == 0 {
			record.Set(field, "")
			return nil
		}
	case int:
		if v == 0 {
			record.Set(field, "")
			return nil
		}
	case string:
		if strings.TrimSpace(v) == "" {
			record.Set(field, "")
			return nil
		}
	}
	pbID := resolvePBRelationID(app, collectionForRelationField(field), value, authID)
	if pbID == "" {
		return fmt.Errorf("unknown %s id", field)
	}
	record.Set(field, pbID)
	return nil
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

	if desc {
		return "-" + pbField
	}
	return pbField
}
