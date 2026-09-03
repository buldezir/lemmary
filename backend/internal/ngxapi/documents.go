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
	"lemmary/backend/internal/fulltext"
	"lemmary/backend/internal/models"
)

var errSearchIndexNotReady = errors.New("search index is not ready")

func handleListDocuments(idx *fulltext.Index) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		page, pageSize := paginationParams(e)
		query := e.Request.URL.Query()

		filters, err := parseDocumentFilters(e.App, e.Auth.Id, query)
		if err != nil {
			return badRequest(e, err.Error())
		}
		// A filter naming something that does not exist matches nothing. It
		// must not fall through to the unfiltered archive -- which is what this
		// endpoint returned for every filter before it understood any of them,
		// so the client rendered the whole library as the answer to "tagged
		// Invoice".
		if filters.impossible {
			return paginatedList(e, 0, page, pageSize, nil)
		}

		exprs := append([]dbx.Expression{dbx.HashExp{"user": e.Auth.Id}}, documentFilterExprs(filters)...)
		ordering := strings.TrimSpace(query.Get("ordering"))

		if filters.hasText() {
			return listDocumentsByText(e, idx, filters, exprs, ordering, page, clampSearchPageSize(pageSize))
		}

		total, err := e.App.CountRecords("documents", exprs...)
		if err != nil {
			return internalError(e, err)
		}

		records := []*core.Record{}
		q := e.App.RecordQuery("documents")
		for _, expr := range exprs {
			q.AndWhere(expr)
		}
		for _, order := range documentSortColumns(ordering) {
			q.AndOrderBy(order)
		}
		if err := q.Limit(int64(pageSize)).Offset(int64((page - 1) * pageSize)).All(&records); err != nil {
			return internalError(e, err)
		}

		results, err := mapDocuments(e.App, records, filters.truncateContent)
		if err != nil {
			return internalError(e, err)
		}
		return paginatedList(e, total, page, pageSize, results)
	}
}

// maxTextIDs caps how many index matches one request enumerates.
//
// The text query and the filters live in different stores, so the only exact
// way to combine them is to enumerate the text matches and intersect. Past the
// cap the count under-reports -- but self-consistently: paginatedList derives
// both totalPages and the next link from that same number, so a client is never
// handed a link to a page that cannot be served. 5000 is what the agent's
// grouped count already uses (appapi.maxCountIDs), and what paperless-ngx
// itself switches intersection strategies at.
const maxTextIDs = 5000

// listDocumentsByText answers a request carrying a text filter: the index says
// which documents match the words, the database says which of those match
// everything else, and the intersection is the page.
//
// The order is the index's unless the client asked for one. Relevance is the
// only ranking a search has, and a client that sent no ordering wants it.
func listDocumentsByText(
	e *core.RequestEvent,
	idx *fulltext.Index,
	filters documentFilters,
	exprs []dbx.Expression,
	ordering string,
	page, pageSize int,
) error {
	if idx == nil || !idx.Ready() {
		return internalError(e, errSearchIndexNotReady)
	}

	ranked, err := textMatchIDs(idx, filters, e.Auth.Id)
	if err != nil {
		return internalError(e, err)
	}
	if len(ranked) == 0 {
		return paginatedList(e, 0, page, pageSize, nil)
	}

	// An id-only scan rather than an IN list of every hit: the survivor set is
	// what both the count and the page need, and binding thousands of
	// parameters to learn it is the cost this avoids.
	survivors, err := filteredDocumentIDs(e.App, exprs, ordering)
	if err != nil {
		return internalError(e, err)
	}

	// Whichever list carries the wanted order drives the walk; the other
	// becomes the membership test.
	source, filter := ranked, survivors
	if ordering != "" {
		source, filter = survivors, ranked
	}
	allowed := make(map[string]struct{}, len(filter))
	for _, id := range filter {
		allowed[id] = struct{}{}
	}

	ordered := make([]string, 0, min(len(source), len(allowed)))
	for _, id := range source {
		if _, ok := allowed[id]; ok {
			ordered = append(ordered, id)
		}
	}

	total := int64(len(ordered))
	offset := (page - 1) * pageSize
	if offset >= len(ordered) {
		return paginatedList(e, total, page, pageSize, nil)
	}
	end := min(offset+pageSize, len(ordered))

	records := make([]*core.Record, 0, end-offset)
	for _, id := range ordered[offset:end] {
		record, err := e.App.FindRecordById("documents", id)
		if err != nil {
			continue
		}
		records = append(records, record)
	}

	results, err := mapDocuments(e.App, records, filters.truncateContent)
	if err != nil {
		return internalError(e, err)
	}
	return paginatedList(e, total, page, pageSize, results)
}

// textMatchIDs runs every text criterion and intersects them, keeping the
// first one's ranking. They are ANDed because that is what a client combining
// `query` with `title__icontains` is asking for.
func textMatchIDs(idx *fulltext.Index, filters documentFilters, authID string) ([]string, error) {
	var ranked []string
	for i, criterion := range filters.text {
		ids, _, _, err := idx.MatchingIDs(fulltext.Query{
			Text:   criterion.text,
			Fields: criterion.fields,
			UserID: authID,
		}, maxTextIDs)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			ranked = ids
			continue
		}
		keep := make(map[string]struct{}, len(ids))
		for _, id := range ids {
			keep[id] = struct{}{}
		}
		kept := ranked[:0]
		for _, id := range ranked {
			if _, ok := keep[id]; ok {
				kept = append(kept, id)
			}
		}
		ranked = kept
	}
	return ranked, nil
}

// filteredDocumentIDs lists the ids matching exprs, in ordering's order.
func filteredDocumentIDs(app core.App, exprs []dbx.Expression, ordering string) ([]string, error) {
	q := app.RecordQuery("documents").Select("[[documents.id]]")
	for _, expr := range exprs {
		q.AndWhere(expr)
	}
	if ordering != "" {
		for _, order := range documentSortColumns(ordering) {
			q.AndOrderBy(order)
		}
	}
	var ids []string
	if err := q.Column(&ids); err != nil {
		return nil, err
	}
	return ids, nil
}

func mapDocuments(app core.App, records []*core.Record, truncate bool) ([]any, error) {
	lens, err := newNgxIDLens(app, records)
	if err != nil {
		return nil, err
	}
	results := make([]any, 0, len(records))
	for _, record := range records {
		results = append(results, mapDocument(lens, record, truncate))
	}
	return results, nil
}

// mapOneDocument is mapDocuments for a single-record response.
func mapOneDocument(app core.App, record *core.Record) (map[string]any, error) {
	lens, err := newNgxIDLens(app, []*core.Record{record})
	if err != nil {
		return nil, err
	}
	return mapDocument(lens, record, false), nil
}

func handleGetDocument(e *core.RequestEvent) error {
	record, err := findOwnedDocument(e.App, e.Auth.Id, e.Request.PathValue("id"))
	if err != nil {
		return notFound(e, "Not found.")
	}
	mapped, err := mapOneDocument(e.App, record)
	if err != nil {
		return internalError(e, err)
	}
	return writeJSON(e, http.StatusOK, mapped)
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

	mapped, err := mapOneDocument(e.App, record)
	if err != nil {
		return internalError(e, err)
	}
	return writeJSON(e, http.StatusOK, mapped)
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

// documentSortColumns maps a paperless ordering to quoted ORDER BY clauses.
//
// The id tiebreaker is not decoration: several documents routinely share a
// created timestamp or a document_date, and SQLite is free to return tied rows
// in any order it likes. Without a total order, paging the same list twice can
// show a row twice and never show another -- the bug is invisible on page one
// and reliable at a page boundary.
func documentSortColumns(ordering string) []string {
	direction := " ASC"
	if strings.HasPrefix(ordering, "-") {
		direction = " DESC"
	}
	field := strings.TrimPrefix(strings.TrimPrefix(ordering, "-"), "+")

	var column string
	switch field {
	case "created", "created_date":
		column = "document_date"
	case "added":
		column = "created"
	case "modified":
		column = "updated"
	case "title":
		column = "title"
	case "id":
		column = "id"
	default:
		// Unknown orderings, and the empty one, fall back to newest first.
		column = "created"
		if ordering == "" {
			direction = " DESC"
		}
	}

	columns := []string{"[[documents." + column + "]]" + direction}
	if column != "id" {
		columns = append(columns, "[[documents.id]]"+direction)
	}
	return columns
}
