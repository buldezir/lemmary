package ngxapi

import (
	"net/http"
	"strings"

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

type namedEntityBody struct {
	Name         string `json:"name"`
	NameOriginal string `json:"name_original"`
}

// createOwnedNamedRecord defaults name_original to name for the collections
// that carry it; tags have no such column.
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

// patchOwnedNamedRecord leaves blank fields unchanged so a partial PATCH cannot
// clear a name.
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

	// Includes what documents shared with the caller carry, so a shared
	// document's tags render as names rather than unknown ids.
	scope, err := readableEntities(e.App, collection, ownerUserID)
	if err != nil {
		return internalError(e, err)
	}
	total, err := e.App.CountRecords(collection, scope)
	if err != nil {
		return internalError(e, err)
	}

	offset := (page - 1) * pageSize
	records := []*core.Record{}
	q := e.App.RecordQuery(collection).AndWhere(scope).AndOrderBy("name ASC")
	if err := q.Limit(int64(pageSize)).Offset(int64(offset)).All(&records); err != nil {
		return internalError(e, err)
	}

	results := make([]any, 0, len(records))
	for _, record := range records {
		results = append(results, mapReadable(mapper, record, ownerUserID))
	}

	return paginatedList(e, total, page, pageSize, results)
}

func getNamedRecord(e *core.RequestEvent, collection string, mapper recordMapper, ownerUserID string) error {
	ngxID, err := parseNgxID(e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	record, err := findReadableNamedRecord(e.App, collection, ngxID, ownerUserID)
	if err != nil {
		return notFound(e, "Not found.")
	}
	return writeJSON(e, http.StatusOK, mapReadable(mapper, record, ownerUserID))
}

// mapReadable tells a client which entries it may edit: the list also carries
// the entities on documents shared with the caller, which stay their owner's,
// and a PATCH or DELETE of one is a 404.
func mapReadable(mapper recordMapper, record *core.Record, callerID string) map[string]any {
	out := mapper(record)
	out["user_can_change"] = record.GetString("user") == callerID
	return out
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
