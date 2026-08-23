package ngxapi

import (
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/strutil"
)

func handleListTags(e *core.RequestEvent) error {
	return listNamedRecords(e, "tags", mapTag, e.Auth.Id)
}

func handleGetTag(e *core.RequestEvent) error {
	return getNamedRecord(e, "tags", mapTag, e.Auth.Id)
}

func handleCreateTag(e *core.RequestEvent) error {
	return createOwnedNamedRecord(e, "tags", mapTag)
}

func handlePatchTag(e *core.RequestEvent) error {
	return patchOwnedNamedRecord(e, "tags", mapTag)
}

func handleDeleteTag(e *core.RequestEvent) error {
	return deleteNamedRecord(e, "tags", e.Auth.Id)
}

func handleListCorrespondents(e *core.RequestEvent) error {
	return listNamedRecords(e, "correspondents", mapCorrespondent, e.Auth.Id)
}

func handleGetCorrespondent(e *core.RequestEvent) error {
	return getNamedRecord(e, "correspondents", mapCorrespondent, e.Auth.Id)
}

func handleCreateCorrespondent(e *core.RequestEvent) error {
	return createOwnedNamedRecord(e, "correspondents", mapCorrespondent)
}

func handlePatchCorrespondent(e *core.RequestEvent) error {
	return patchOwnedNamedRecord(e, "correspondents", mapCorrespondent)
}

func handleDeleteCorrespondent(e *core.RequestEvent) error {
	return deleteNamedRecord(e, "correspondents", e.Auth.Id)
}

func handleListDocumentTypes(e *core.RequestEvent) error {
	return listNamedRecords(e, "document_types", mapDocumentType, e.Auth.Id)
}

func handleGetDocumentType(e *core.RequestEvent) error {
	return getNamedRecord(e, "document_types", mapDocumentType, e.Auth.Id)
}

func handleCreateDocumentType(e *core.RequestEvent) error {
	return createOwnedNamedRecord(e, "document_types", mapDocumentType)
}

func handlePatchDocumentType(e *core.RequestEvent) error {
	return patchOwnedNamedRecord(e, "document_types", mapDocumentType)
}

func handleDeleteDocumentType(e *core.RequestEvent) error {
	return deleteNamedRecord(e, "document_types", e.Auth.Id)
}

// namedEntityBody is the write payload shared by correspondents and document types.
type namedEntityBody struct {
	Name         string `json:"name"`
	NameOriginal string `json:"name_original"`
}

// createOwnedNamedRecord creates a user-owned named entity (tag, correspondent,
// or document type). For collections that carry name_original it defaults to
// name when the client omits it; tags have no such column.
func createOwnedNamedRecord(e *core.RequestEvent, collection string, mapper recordMapper) error {
	var body namedEntityBody
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return badRequest(e, "Name is required.")
	}
	original := strutil.FirstNonEmpty(body.NameOriginal, name)

	coll, err := e.App.FindCollectionByNameOrId(collection)
	if err != nil {
		return internalError(e, err)
	}

	record := core.NewRecord(coll)
	record.Set("user", e.Auth.Id)
	record.Set("name", name)
	if coll.Fields.GetByName("name_original") != nil {
		record.Set("name_original", original)
	}
	if err := e.App.Save(record); err != nil {
		return saveError(e, err)
	}

	return writeJSON(e, http.StatusCreated, mapper(record))
}

// patchOwnedNamedRecord updates a user-owned named entity. Blank fields are left
// unchanged so a partial PATCH cannot clear a name.
func patchOwnedNamedRecord(e *core.RequestEvent, collection string, mapper recordMapper) error {
	ngxID, err := parseNgxID(e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	record, err := findRecordByNgxID(e.App, collection, ngxID, e.Auth.Id)
	if err != nil {
		return notFound(e, "Not found.")
	}

	var body namedEntityBody
	if err := e.BindBody(&body); err != nil {
		return badRequest(e, "Invalid request body.")
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		record.Set("name", name)
	}
	if original := strings.TrimSpace(body.NameOriginal); original != "" && record.Collection().Fields.GetByName("name_original") != nil {
		record.Set("name_original", original)
	}
	if err := e.App.Save(record); err != nil {
		return saveError(e, err)
	}

	return writeJSON(e, http.StatusOK, mapper(record))
}

type recordMapper func(*core.Record) map[string]any

func listNamedRecords(e *core.RequestEvent, collection string, mapper recordMapper, ownerUserID string) error {
	page, pageSize := paginationParams(e)

	filter, params := ownerScope(ownerUserID)
	total, err := e.App.CountRecords(collection, dbx.HashExp{"user": ownerUserID})
	if err != nil {
		return internalError(e, err)
	}

	offset := (page - 1) * pageSize
	records, err := e.App.FindRecordsByFilter(collection, filter, "name", pageSize, offset, params)
	if err != nil {
		return internalError(e, err)
	}

	results := make([]any, 0, len(records))
	for _, record := range records {
		results = append(results, mapper(record))
	}

	return paginatedList(e, total, page, pageSize, results)
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
