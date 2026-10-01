package appapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/backup"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/strutil"
)

const exportPageSize = 100

type exportRequest struct {
	IDs []string `json:"ids"`
}

// handleExportDocuments writes the archive POST /api/app/import/archive
// restores from: the caller's whole library, or given ids, those of them the
// caller can read.
func handleExportDocuments(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		// Superuser sessions export their paired user's archive; e.Auth.Id is
		// the _superusers record id, which matches no document at all.
		userID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		var req exportRequest
		if err := json.NewDecoder(e.Request.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			return writeError(e, http.StatusBadRequest, "Invalid request body.")
		}

		var records []*core.Record
		if req.IDs == nil {
			records, err = listOwnedDocuments(app, userID)
		} else {
			records, err = listReadableDocuments(app, userID, req.IDs)
		}
		if err != nil {
			app.Logger().Error("export list documents failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to list documents.")
		}
		taxonomy, index, err := exportTaxonomy(app, userID, records, req.IDs == nil)
		if err != nil {
			app.Logger().Error("export list taxonomy failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to list tags.")
		}

		fsys, err := app.NewFilesystem()
		if err != nil {
			app.Logger().Error("export open storage failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to open storage.")
		}
		defer fsys.Close()

		docs := make([]backup.Document, 0, len(records))
		for _, record := range records {
			fileName := record.GetString("file")
			if fileName == "" {
				continue
			}
			rec := record
			doc := backup.Document{
				ID:               rec.Id,
				Title:            strutil.FirstNonEmpty(rec.GetString("title"), "Untitled"),
				Date:             truncateDate(rec.GetString("document_date")),
				OriginalFilename: fileName,
				OpenFile:         openStoredFile(app, fsys, rec, fileName),
				OCRText:          rec.GetString("ocr_text"),
				Metadata:         buildExportMetadata(rec, index, fileName),
			}
			if previewName := rec.GetString("preview"); previewName != "" {
				doc.OpenPreview = openStoredFile(app, fsys, rec, previewName)
			}
			docs = append(docs, doc)
		}

		e.Response.Header().Set("Content-Type", "application/zip")
		e.Response.Header().Set("Content-Disposition", `attachment; filename="lemmary-export.zip"`)
		e.Response.WriteHeader(http.StatusOK)

		if err := backup.Write(e.Response, backup.Archive{Documents: docs, Taxonomy: taxonomy}); err != nil {
			app.Logger().Error("export zip failed", "error", err)
			return err
		}
		return nil
	}
}

// A blob missing from storage is logged and skips its entry rather than
// failing the whole backup.
func openStoredFile(app core.App, fsys *filesystem.System, record *core.Record, fileName string) func() (io.ReadCloser, error) {
	key := record.BaseFilesPath() + "/" + fileName
	return func() (io.ReadCloser, error) {
		reader, err := fsys.GetReader(key)
		if err != nil {
			app.Logger().Warn("export skip missing file", "document", record.Id, "file", fileName, "error", err)
			return nil, err
		}
		return reader, nil
	}
}

func listOwnedDocuments(app core.App, userID string) ([]*core.Record, error) {
	return listOwnedRecords(app, "documents", userID, "-created")
}

func listReadableDocuments(app core.App, userID string, ids []string) ([]*core.Record, error) {
	readable := dbx.NewExp(ReadableDocumentsSQL("documents", "reader"), dbx.Params{"reader": userID})
	return findRecordsByIDs(app, "documents", ids, func(q *dbx.SelectQuery) error {
		q.AndWhere(readable)
		return nil
	})
}

// findRecordsByIDs asks in chunks so a long list stays under SQLite's
// bound-parameter limit.
func findRecordsByIDs(app core.App, collection string, ids []string, filters ...func(*dbx.SelectQuery) error) ([]*core.Record, error) {
	var all []*core.Record
	for chunk := range slices.Chunk(slices.Compact(slices.Sorted(slices.Values(ids))), exportPageSize) {
		records, err := app.FindRecordsByIds(collection, chunk, filters...)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", collection, err)
		}
		all = append(all, records...)
	}
	return all, nil
}

// exportTaxonomy is everything the archive names the documents' relations
// and values by: the caller's whole taxonomy for a full export, else only what
// the documents carry.
func exportTaxonomy(app core.App, userID string, records []*core.Record, whole bool) (backup.Taxonomy, taxonomyIndex, error) {
	values, err := models.LoadFieldValues(app, recordIDs(records)...)
	if err != nil {
		return backup.Taxonomy{}, taxonomyIndex{}, err
	}
	var taxonomy backup.Taxonomy
	var index taxonomyIndex
	if whole {
		taxonomy, index, err = listOwnedTaxonomy(app, userID)
	} else {
		taxonomy, index, err = listReferencedTaxonomy(app, userID, records, values)
	}
	if err != nil {
		return taxonomy, index, err
	}
	index.values = values
	index.customFields, err = customFieldNamesByID(app)
	return taxonomy, index, err
}

func recordIDs(records []*core.Record) []string {
	ids := make([]string, len(records))
	for i, record := range records {
		ids[i] = record.Id
	}
	return ids
}

func listOwnedRecords(app core.App, collection, userID, sort string) ([]*core.Record, error) {
	return listRecordsWhere(app, collection, "user = {:userId}", dbx.Params{"userId": userID}, sort)
}

func listRecordsWhere(app core.App, collection, filter string, params dbx.Params, sort string) ([]*core.Record, error) {
	var all []*core.Record
	page := 1
	for {
		records, err := app.FindRecordsByFilter(
			collection,
			filter,
			sort,
			exportPageSize,
			(page-1)*exportPageSize,
			params,
		)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", collection, err)
		}
		all = append(all, records...)
		if len(records) < exportPageSize {
			break
		}
		page++
	}
	return all, nil
}

// taxonomyIndex resolves ids to names while packing, so a few hundred
// documents do not re-read the same tag record each.
type taxonomyIndex struct {
	tags         map[string]string
	customFields map[string]string
	values       map[string]models.FieldValues
}

// listOwnedTaxonomy includes records no document references on purpose: they
// exist only here, and a restore that dropped them would lose part of the
// library.
func listOwnedTaxonomy(app core.App, userID string) (backup.Taxonomy, taxonomyIndex, error) {
	return collectTaxonomy(func(kind string) ([]*core.Record, error) {
		if kind == "tags" {
			return listOwnedRecords(app, kind, userID, "name")
		}
		return listRecordsWhere(app, models.CustomFieldOptionsCollection, "field = {:field} && user = {:userId}",
			dbx.Params{"field": kind, "userId": userID}, "name")
	})
}

// listReferencedTaxonomy takes whatever the documents carry, whoever owns it: a
// shared document's tags are its owner's. The caller's own records come first,
// so on a name both owners use, theirs is the one the archive keeps.
func listReferencedTaxonomy(app core.App, userID string, documents []*core.Record, values map[string]models.FieldValues) (backup.Taxonomy, taxonomyIndex, error) {
	ids := map[string][]string{}
	for _, doc := range documents {
		ids["tags"] = append(ids["tags"], doc.GetStringSlice("tags")...)
		for _, field := range models.PredefinedCustomFields {
			if id := values[doc.Id].OptionID(field.ID); id != "" {
				ids[field.ID] = append(ids[field.ID], id)
			}
		}
	}
	ownFirst := func(record *core.Record) string {
		if record.GetString("user") == userID {
			return "0" + record.Id
		}
		return "1" + record.Id
	}
	return collectTaxonomy(func(kind string) ([]*core.Record, error) {
		collection := models.CustomFieldOptionsCollection
		if kind == "tags" {
			collection = kind
		}
		records, err := findRecordsByIDs(app, collection, ids[kind])
		slices.SortFunc(records, func(a, b *core.Record) int {
			return strings.Compare(ownFirst(a), ownFirst(b))
		})
		return records, err
	})
}

// collectTaxonomy lists each name once, since two owners can each have an
// "invoice" tag. list is asked for "tags" and for each predefined field's id.
func collectTaxonomy(list func(kind string) ([]*core.Record, error)) (backup.Taxonomy, taxonomyIndex, error) {
	index := taxonomyIndex{tags: map[string]string{}}
	taxonomy := backup.Taxonomy{
		Tags:           []string{},
		Correspondents: []backup.NamedEntity{},
		DocumentTypes:  []backup.NamedEntity{},
	}

	tags, err := list("tags")
	if err != nil {
		return taxonomy, index, err
	}
	seen := map[string]bool{}
	for _, tag := range tags {
		name := strings.TrimSpace(tag.GetString("name"))
		if name == "" {
			continue
		}
		index.tags[tag.Id] = name
		if !seen[name] {
			seen[name] = true
			taxonomy.Tags = append(taxonomy.Tags, name)
		}
	}

	for _, group := range []struct {
		fieldID string
		into    *[]backup.NamedEntity
	}{
		{models.CorrespondentFieldID, &taxonomy.Correspondents},
		{models.DocumentTypeFieldID, &taxonomy.DocumentTypes},
	} {
		records, err := list(group.fieldID)
		if err != nil {
			return taxonomy, index, err
		}
		seen := map[string]bool{}
		for _, record := range records {
			name := strings.TrimSpace(record.GetString("name"))
			if name == "" {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			*group.into = append(*group.into, backup.NamedEntity{Name: name})
		}
	}

	return taxonomy, index, nil
}

func customFieldNamesByID(app core.App) (map[string]string, error) {
	fields, err := models.LoadCustomFields(app)
	if err != nil {
		return nil, err
	}
	names := make(map[string]string, len(fields))
	for _, field := range fields {
		names[field.ID] = field.Name
	}
	return names, nil
}

// exportCustomFields keys values by field name, like the relations: field ids
// are this instance's own. names holds the admin's fields only, so the
// predefined ones stay in their own sidecar keys.
func exportCustomFields(values models.FieldValues, names map[string]string) map[string]any {
	out := map[string]any{}
	for id, value := range values {
		if name := names[id]; name != "" {
			out[name] = value.Value()
		}
	}
	return out
}

// buildExportMetadata writes relations as names, not ids: ids mean nothing in
// the instance the archive is restored into.
func buildExportMetadata(record *core.Record, index taxonomyIndex, originalFilename string) map[string]any {
	tags := make([]string, 0)
	for _, tagID := range record.GetStringSlice("tags") {
		if name := index.tags[tagID]; name != "" {
			tags = append(tags, name)
		}
	}

	return map[string]any{
		"id":                      record.Id,
		"title":                   record.GetString("title"),
		"summary":                 record.GetString("summary"),
		"document_date":           record.GetString("document_date"),
		"tags":                    tags,
		"document_type":           index.values[record.Id].OptionName(models.DocumentTypeFieldID),
		"correspondent":           index.values[record.Id].OptionName(models.CorrespondentFieldID),
		"people_or_organizations": models.PeopleOrOrganizations(record),
		"custom_fields":           exportCustomFields(index.values[record.Id], index.customFields),
		"processing_status":       record.GetString("processing_status"),
		"metadata_source":         record.GetString("metadata_source"),
		"confidence":              record.GetFloat("confidence"),
		"checksum":                record.GetString("checksum"),
		"text_fingerprint":        record.GetString("text_fingerprint"),
		// The exporting instance's id for the near-duplicate original. Import
		// remaps it, or drops it when that document is not in the archive.
		"duplicate_of":      record.GetString("duplicate_of"),
		"created":           record.GetString("created"),
		"updated":           record.GetString("updated"),
		"original_filename": originalFilename,
	}
}
