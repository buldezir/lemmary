package ngxapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

func handleListTags(e *core.RequestEvent) error {
	return listNamedRecords(e, "tags", mapTag, "")
}

func handleGetTag(e *core.RequestEvent) error {
	return getNamedRecord(e, "tags", mapTag, "")
}

func handleCreateTag(e *core.RequestEvent) error {
	return createNamedRecord(e, "tags", mapTag)
}

func handlePatchTag(e *core.RequestEvent) error {
	return patchNamedRecord(e, "tags", mapTag, "")
}

func handleDeleteTag(e *core.RequestEvent) error {
	return deleteNamedRecord(e, "tags", "")
}

func handleListCorrespondents(e *core.RequestEvent) error {
	return listNamedRecords(e, "correspondents", mapCorrespondent, e.Auth.Id)
}

func handleGetCorrespondent(e *core.RequestEvent) error {
	return getNamedRecord(e, "correspondents", mapCorrespondent, e.Auth.Id)
}

func handleCreateCorrespondent(e *core.RequestEvent) error {
	return createCorrespondentRecord(e, mapCorrespondent)
}

func handlePatchCorrespondent(e *core.RequestEvent) error {
	return patchCorrespondentRecord(e, mapCorrespondent)
}

func handleDeleteCorrespondent(e *core.RequestEvent) error {
	return deleteNamedRecord(e, "correspondents", e.Auth.Id)
}

func createCorrespondentRecord(e *core.RequestEvent, mapper recordMapper) error {
	var body struct {
		Name         string `json:"name"`
		NameOriginal string `json:"name_original"`
	}
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return badRequest(e, "Name is required.")
	}
	original := strings.TrimSpace(body.NameOriginal)
	if original == "" {
		original = name
	}

	coll, err := e.App.FindCollectionByNameOrId("correspondents")
	if err != nil {
		return internalError(e, err)
	}

	record := core.NewRecord(coll)
	record.Set("user", e.Auth.Id)
	record.Set("name", name)
	record.Set("name_original", original)
	if err := e.App.Save(record); err != nil {
		return badRequest(e, err.Error())
	}

	return writeJSON(e, http.StatusCreated, mapper(record))
}

func patchCorrespondentRecord(e *core.RequestEvent, mapper recordMapper) error {
	ngxID, err := parseNgxID(e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	record, err := findRecordByNgxID(e.App, "correspondents", ngxID, e.Auth.Id)
	if err != nil {
		return notFound(e, "Not found.")
	}

	var body struct {
		Name         string `json:"name"`
		NameOriginal string `json:"name_original"`
	}
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		record.Set("name", name)
	}
	if original := strings.TrimSpace(body.NameOriginal); original != "" {
		record.Set("name_original", original)
	}
	if err := e.App.Save(record); err != nil {
		return badRequest(e, err.Error())
	}

	return writeJSON(e, http.StatusOK, mapper(record))
}

func handleListDocumentTypes(e *core.RequestEvent) error {
	return listNamedRecords(e, "document_types", mapDocumentType, e.Auth.Id)
}

func handleGetDocumentType(e *core.RequestEvent) error {
	return getNamedRecord(e, "document_types", mapDocumentType, e.Auth.Id)
}

func handleCreateDocumentType(e *core.RequestEvent) error {
	return createDocumentTypeRecord(e, mapDocumentType)
}

func handlePatchDocumentType(e *core.RequestEvent) error {
	return patchDocumentTypeRecord(e, mapDocumentType)
}

func createDocumentTypeRecord(e *core.RequestEvent, mapper recordMapper) error {
	var body struct {
		Name         string `json:"name"`
		NameOriginal string `json:"name_original"`
	}
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return badRequest(e, "Name is required.")
	}
	original := strings.TrimSpace(body.NameOriginal)
	if original == "" {
		original = name
	}

	coll, err := e.App.FindCollectionByNameOrId("document_types")
	if err != nil {
		return internalError(e, err)
	}

	record := core.NewRecord(coll)
	record.Set("user", e.Auth.Id)
	record.Set("name", name)
	record.Set("name_original", original)
	if err := e.App.Save(record); err != nil {
		return badRequest(e, err.Error())
	}

	return writeJSON(e, http.StatusCreated, mapper(record))
}

func patchDocumentTypeRecord(e *core.RequestEvent, mapper recordMapper) error {
	ngxID, err := parseNgxID(e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	record, err := findRecordByNgxID(e.App, "document_types", ngxID, e.Auth.Id)
	if err != nil {
		return notFound(e, "Not found.")
	}

	var body struct {
		Name         string `json:"name"`
		NameOriginal string `json:"name_original"`
	}
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		record.Set("name", name)
	}
	if original := strings.TrimSpace(body.NameOriginal); original != "" {
		record.Set("name_original", original)
	}
	if err := e.App.Save(record); err != nil {
		return badRequest(e, err.Error())
	}

	return writeJSON(e, http.StatusOK, mapper(record))
}

func handleDeleteDocumentType(e *core.RequestEvent) error {
	return deleteNamedRecord(e, "document_types", e.Auth.Id)
}

type recordMapper func(*core.Record) map[string]any

func listNamedRecords(e *core.RequestEvent, collection string, mapper recordMapper, ownerUserID string) error {
	page, pageSize := paginationParams(e)

	var (
		total  int64
		err    error
		filter string
		params map[string]any
	)
	if ownerUserID != "" {
		filter, params = ownerScope(ownerUserID)
		total, err = e.App.CountRecords(collection, dbx.HashExp{"user": ownerUserID})
	} else {
		total, err = e.App.CountRecords(collection)
	}
	if err != nil {
		return internalError(e, err)
	}

	offset := (page - 1) * pageSize
	records, err := findNamedRecordsPage(e.App, collection, filter, pageSize, offset, params)
	if err != nil {
		return internalError(e, err)
	}

	results := make([]any, 0, len(records))
	for _, record := range records {
		results = append(results, mapper(record))
	}

	return paginatedList(e, total, page, pageSize, results)
}

func findNamedRecordsPage(app core.App, collection, filter string, pageSize, offset int, params map[string]any) ([]*core.Record, error) {
	if params != nil {
		return app.FindRecordsByFilter(collection, filter, "name", pageSize, offset, params)
	}
	return app.FindRecordsByFilter(collection, filter, "name", pageSize, offset)
}

func getNamedRecord(e *core.RequestEvent, collection string, mapper recordMapper, ownerUserID string) error {
	ngxID, err := parseNgxID(e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	record, err := findRecordByNgxID(e.App, collection, ngxID, ownerUserID)
	if err != nil {
		return notFound(e, "Not found.")
	}
	return writeJSON(e, http.StatusOK, mapper(record))
}

func createNamedRecord(e *core.RequestEvent, collection string, mapper recordMapper) error {
	var body struct {
		Name string `json:"name"`
	}
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return badRequest(e, "Name is required.")
	}

	coll, err := e.App.FindCollectionByNameOrId(collection)
	if err != nil {
		return internalError(e, err)
	}

	record := core.NewRecord(coll)
	record.Set("name", name)
	if err := e.App.Save(record); err != nil {
		return badRequest(e, err.Error())
	}

	return writeJSON(e, http.StatusCreated, mapper(record))
}

func patchNamedRecord(e *core.RequestEvent, collection string, mapper recordMapper, ownerUserID string) error {
	ngxID, err := parseNgxID(e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	record, err := findRecordByNgxID(e.App, collection, ngxID, ownerUserID)
	if err != nil {
		return notFound(e, "Not found.")
	}

	var body struct {
		Name string `json:"name"`
	}
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		record.Set("name", name)
	}
	if err := e.App.Save(record); err != nil {
		return badRequest(e, err.Error())
	}

	return writeJSON(e, http.StatusOK, mapper(record))
}

func deleteNamedRecord(e *core.RequestEvent, collection, ownerUserID string) error {
	ngxID, err := parseNgxID(e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	record, err := findRecordByNgxID(e.App, collection, ngxID, ownerUserID)
	if err != nil {
		return notFound(e, "Not found.")
	}
	if err := e.App.Delete(record); err != nil {
		return internalError(e, err)
	}
	e.Response.WriteHeader(http.StatusNoContent)
	return nil
}
