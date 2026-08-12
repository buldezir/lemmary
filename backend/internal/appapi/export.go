package appapi

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

const exportPageSize = 100

func handleExportDocuments(app core.App) func(*core.RequestEvent) error {
	return func(e *core.RequestEvent) error {
		mode, err := ParseExportMode(e.Request.URL.Query().Get("mode"))
		if err != nil {
			return writeError(e, http.StatusBadRequest, err.Error())
		}

		userID := e.Auth.Id
		records, err := listOwnedDocuments(app, userID)
		if err != nil {
			app.Logger().Error("export list documents failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to list documents.")
		}

		fsys, err := app.NewFilesystem()
		if err != nil {
			app.Logger().Error("export open storage failed", "error", err)
			return writeError(e, http.StatusInternalServerError, "Failed to open storage.")
		}
		defer fsys.Close()

		includeMetadata := mode == ExportModeMetadata
		docs := make([]ExportDocument, 0, len(records))
		for _, record := range records {
			fileName := record.GetString("file")
			if fileName == "" {
				continue
			}
			rec := record
			name := fileName
			fileKey := rec.BaseFilesPath() + "/" + name
			doc := ExportDocument{
				ID:               rec.Id,
				Title:            firstNonEmpty(rec.GetString("title"), rec.GetString("title_original"), "Untitled"),
				OriginalFilename: name,
				OpenFile: func() (io.ReadCloser, error) {
					reader, err := fsys.GetReader(fileKey)
					if err != nil {
						app.Logger().Warn("export skip missing file", "document", rec.Id, "error", err)
						return nil, err
					}
					return reader, nil
				},
				OCRText: rec.GetString("ocr_text"),
			}
			if includeMetadata {
				doc.Metadata = buildExportMetadata(app, rec, name)
			}
			docs = append(docs, doc)
		}

		e.Response.Header().Set("Content-Type", "application/zip")
		e.Response.Header().Set("Content-Disposition", `attachment; filename="paperless-export.zip"`)
		e.Response.WriteHeader(http.StatusOK)

		if err := WriteExportZip(e.Response, mode, docs); err != nil {
			app.Logger().Error("export zip failed", "error", err)
			return err
		}
		return nil
	}
}

func listOwnedDocuments(app core.App, userID string) ([]*core.Record, error) {
	var all []*core.Record
	page := 1
	for {
		records, err := app.FindRecordsByFilter(
			"documents",
			"user = {:userId}",
			"-created",
			exportPageSize,
			(page-1)*exportPageSize,
			dbx.Params{"userId": userID},
		)
		if err != nil {
			return nil, fmt.Errorf("list documents: %w", err)
		}
		all = append(all, records...)
		if len(records) < exportPageSize {
			break
		}
		page++
	}
	return all, nil
}

func buildExportMetadata(app core.App, record *core.Record, originalFilename string) map[string]any {
	tags := make([]string, 0)
	for _, tagID := range record.GetStringSlice("tags") {
		if tagID == "" {
			continue
		}
		tagRec, err := app.FindRecordById("tags", tagID)
		if err != nil {
			continue
		}
		if name := strings.TrimSpace(tagRec.GetString("name")); name != "" {
			tags = append(tags, name)
		}
	}

	documentType := ""
	if typeID := record.GetString("document_type"); typeID != "" {
		if typeRec, err := app.FindRecordById("document_types", typeID); err == nil {
			documentType = typeRec.GetString("name")
		}
	}

	correspondent := ""
	if corrID := record.GetString("correspondent"); corrID != "" {
		if corrRec, err := app.FindRecordById("correspondents", corrID); err == nil {
			correspondent = corrRec.GetString("name")
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
		"document_type":           documentType,
		"correspondent":           correspondent,
		"people_or_organizations": peopleOrOrganizations(record),
		"processing_status":       record.GetString("processing_status"),
		"metadata_source":         record.GetString("metadata_source"),
		"confidence":              record.GetFloat("confidence"),
		"checksum":                record.GetString("checksum"),
		"created":                 record.GetString("created"),
		"updated":                 record.GetString("updated"),
		"original_filename":       originalFilename,
	}
}

func peopleOrOrganizations(record *core.Record) []string {
	raw := record.Get("people_or_organizations")
	switch v := raw.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if strings.TrimSpace(v) == "" {
			return []string{}
		}
		var out []string
		if err := json.Unmarshal([]byte(v), &out); err == nil {
			return out
		}
		return []string{}
	case nil:
		return []string{}
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return []string{}
		}
		var out []string
		if err := json.Unmarshal(b, &out); err != nil {
			return []string{}
		}
		return out
	}
}
