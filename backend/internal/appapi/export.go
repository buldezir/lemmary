package appapi

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/backup"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/strutil"
)

const exportPageSize = 100

// handleExportDocuments streams a full backup of the caller's library: every
// document with its OCR text, metadata and thumbnail, plus the taxonomy they
// reference and the taxonomy they do not. It is the archive
// POST /api/app/import/archive restores from.
func handleExportDocuments(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		// Superuser sessions export their paired user's archive; e.Auth.Id would
		// be the _superusers record id, which matches no document and would
		// yield a silently empty zip.
		userID, err := resolveOwnerUserID(app, e)
		if err != nil {
			return writeOwnerError(e, err)
		}
		records, err := listOwnedDocuments(app, userID)
		if err != nil {
			app.Logger().Error("export list documents failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to list documents.")
		}
		taxonomy, index, err := listOwnedTaxonomy(app, userID)
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
				Title:            strutil.FirstNonEmpty(rec.GetString("title"), rec.GetString("title_original"), "Untitled"),
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

// openStoredFile returns a reader factory for one of a record's file fields.
// A blob missing from storage is logged and skips its entry rather than failing
// the whole backup.
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

// listOwnedRecords pages a user-owned collection into one slice.
func listOwnedRecords(app core.App, collection, userID, sort string) ([]*core.Record, error) {
	var all []*core.Record
	page := 1
	for {
		records, err := app.FindRecordsByFilter(
			collection,
			"user = {:userId}",
			sort,
			exportPageSize,
			(page-1)*exportPageSize,
			dbx.Params{"userId": userID},
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

// taxonomyIndex resolves relation ids to names while packing, so a library with
// a few hundred documents does not re-read the same tag record per document.
type taxonomyIndex struct {
	tags           map[string]string
	correspondents map[string]string
	documentTypes  map[string]string
}

// listOwnedTaxonomy returns the user's whole taxonomy for the manifest and an
// index for resolving document relations. Records no document references are
// included on purpose: they exist only here, and a restore that dropped them
// would quietly lose part of the library.
func listOwnedTaxonomy(app core.App, userID string) (backup.Taxonomy, taxonomyIndex, error) {
	index := taxonomyIndex{
		tags:           map[string]string{},
		correspondents: map[string]string{},
		documentTypes:  map[string]string{},
	}
	taxonomy := backup.Taxonomy{
		Tags:           []string{},
		Correspondents: []backup.NamedEntity{},
		DocumentTypes:  []backup.NamedEntity{},
	}

	tags, err := listOwnedRecords(app, "tags", userID, "name")
	if err != nil {
		return taxonomy, index, err
	}
	for _, tag := range tags {
		name := strings.TrimSpace(tag.GetString("name"))
		if name == "" {
			continue
		}
		index.tags[tag.Id] = name
		taxonomy.Tags = append(taxonomy.Tags, name)
	}

	for _, group := range []struct {
		collection string
		index      map[string]string
		into       *[]backup.NamedEntity
	}{
		{"correspondents", index.correspondents, &taxonomy.Correspondents},
		{"document_types", index.documentTypes, &taxonomy.DocumentTypes},
	} {
		records, err := listOwnedRecords(app, group.collection, userID, "name")
		if err != nil {
			return taxonomy, index, err
		}
		for _, record := range records {
			name := strings.TrimSpace(record.GetString("name"))
			if name == "" {
				continue
			}
			group.index[record.Id] = name
			*group.into = append(*group.into, backup.NamedEntity{
				Name:         name,
				NameOriginal: strings.TrimSpace(record.GetString("name_original")),
			})
		}
	}

	return taxonomy, index, nil
}

// buildExportMetadata is the sidecar a restore reads a document back from.
// Relations are written as names, not ids: ids mean nothing in the instance the
// archive is restored into.
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
		"title_original":          record.GetString("title_original"),
		"purpose":                 record.GetString("purpose"),
		"purpose_original":        record.GetString("purpose_original"),
		"summary":                 record.GetString("summary"),
		"summary_original":        record.GetString("summary_original"),
		"document_date":           record.GetString("document_date"),
		"tags":                    tags,
		"document_type":           index.documentTypes[record.GetString("document_type")],
		"correspondent":           index.correspondents[record.GetString("correspondent")],
		"people_or_organizations": models.PeopleOrOrganizations(record),
		"processing_status":       record.GetString("processing_status"),
		"metadata_source":         record.GetString("metadata_source"),
		"confidence":              record.GetFloat("confidence"),
		"checksum":                record.GetString("checksum"),
		"text_fingerprint":        record.GetString("text_fingerprint"),
		// The exporting instance's id for the near-duplicate original. Import
		// remaps it to the restored record, or drops it when that document is
		// not in the archive.
		"duplicate_of":      record.GetString("duplicate_of"),
		"created":           record.GetString("created"),
		"updated":           record.GetString("updated"),
		"original_filename": originalFilename,
	}
}
