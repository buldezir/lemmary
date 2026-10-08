package remoteimport

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/importjob"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/worker"
)

const maxReportedErrors = 25

const (
	ModePreserve  = "preserve"
	ModeReprocess = "reprocess"
)

var ErrImportInProgress = importjob.ErrBusy

type Result struct {
	Imported               int      `json:"imported"`
	SkippedDuplicates      int      `json:"skipped_duplicates"`
	Failed                 int      `json:"failed"`
	TagsUpserted           int      `json:"tags_upserted"`
	CorrespondentsUpserted int      `json:"correspondents_upserted"`
	DocumentTypesUpserted  int      `json:"document_types_upserted"`
	Errors                 []string `json:"errors"`
}

// ParseMode validates an import mode; empty defaults to preserve.
func ParseMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ModePreserve:
		return ModePreserve, nil
	case ModeReprocess:
		return ModeReprocess, nil
	default:
		return "", fmt.Errorf("mode must be %q or %q", ModePreserve, ModeReprocess)
	}
}

// RunWithClient allows only one import at a time per owner. A nil client is
// built from baseURL and apiKey.
func RunWithClient(app core.App, ownerUserID, baseURL, apiKey, mode string, client *Client) (Result, error) {
	if err := registry.Acquire(ownerUserID); err != nil {
		return Result{}, err
	}
	defer registry.Release(ownerUserID)
	return runImport(app, ownerUserID, baseURL, apiKey, mode, client)
}

func runImport(app core.App, ownerUserID, baseURL, apiKey, mode string, client *Client) (Result, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return Result{}, fmt.Errorf("owner user id is required")
	}
	parsedMode, err := ParseMode(mode)
	if err != nil {
		return Result{}, err
	}

	if client == nil {
		client, err = NewClient(baseURL, apiKey, nil)
		if err != nil {
			return Result{}, err
		}
	}

	result := Result{Errors: []string{}}

	var tagMap, corrMap, typeMap map[int]string
	if parsedMode == ModePreserve {
		tagMap, result.TagsUpserted, err = importNamedEntities(app, "tags", ownerUserID, client.ListTags)
		if err != nil {
			return result, fmt.Errorf("import tags: %w", err)
		}

		corrMap, result.CorrespondentsUpserted, err = importNamedEntities(app, models.CorrespondentFieldID, ownerUserID, client.ListCorrespondents)
		if err != nil {
			return result, fmt.Errorf("import correspondents: %w", err)
		}

		typeMap, result.DocumentTypesUpserted, err = importNamedEntities(app, models.DocumentTypeFieldID, ownerUserID, client.ListDocumentTypes)
		if err != nil {
			return result, fmt.Errorf("import document types: %w", err)
		}
	} else {
		tagMap, corrMap, typeMap = map[int]string{}, map[int]string{}, map[int]string{}
	}

	err = client.ForEachDocuments(func(docs []ngxDocument) error {
		for _, doc := range docs {
			err := importOneDocument(app, client, ownerUserID, parsedMode, doc, tagMap, corrMap, typeMap)
			result.count(fmt.Sprintf("document %d (%s)", doc.ID, strings.TrimSpace(doc.Title)), err)
		}
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("list documents: %w", err)
	}

	return result, nil
}

// importNamedEntities takes "tags" or an option field's id as kind.
func importNamedEntities(app core.App, kind, ownerUserID string, list func() ([]namedEntity, error)) (map[int]string, int, error) {
	entities, err := list()
	if err != nil {
		return nil, 0, err
	}
	idMap := make(map[int]string, len(entities))
	createdCount := 0
	for _, entity := range entities {
		name := strings.TrimSpace(entity.Name)
		if entity.ID == 0 || name == "" {
			continue
		}
		localID, created, err := ensureNamed(app, kind, ownerUserID, name)
		if err != nil {
			return nil, createdCount, err
		}
		idMap[entity.ID] = localID
		if created {
			createdCount++
		}
	}
	return idMap, createdCount, nil
}

func ensureNamed(app core.App, kind, ownerUserID, name string) (id string, created bool, err error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", false, nil
	}
	switch kind {
	case "tags":
		return worker.EnsureTag(app, ownerUserID, name)
	case models.CorrespondentFieldID, models.DocumentTypeFieldID:
		return worker.EnsureOption(app, kind, ownerUserID, name)
	default:
		return "", false, fmt.Errorf("unsupported kind %q", kind)
	}
}

func importOneDocument(
	app core.App,
	client *Client,
	ownerUserID string,
	mode string,
	doc ngxDocument,
	tagMap, corrMap, typeMap map[int]string,
) error {
	file, err := client.DownloadDocument(doc.ID)
	if err != nil {
		return err
	}
	var preserve func(*core.Record) []models.FieldWrite
	if mode == ModePreserve {
		preserve = func(record *core.Record) []models.FieldWrite {
			return applyPreservedMetadata(record, doc, tagMap, corrMap, typeMap)
		}
	}
	return saveDocument(app, ownerUserID, documentFilename(doc, file), file.Data, preserve)
}

// saveDocument stores one downloaded file as a new document. A non-nil preserve
// fills the remote metadata and returns the option values to write once the
// document exists; nil queues the full pipeline as for an upload.
func saveDocument(app core.App, ownerUserID, filename string, data []byte, preserve func(*core.Record) []models.FieldWrite) error {
	if err := rejectKnownChecksum(app, ownerUserID, data); err != nil {
		return err
	}

	fsFile, err := filesystem.NewFileFromBytes(data, filename)
	if err != nil {
		return fmt.Errorf("prepare file: %w", err)
	}

	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}

	record := core.NewRecord(collection)
	record.Set("user", ownerUserID)
	record.Set("file", fsFile)
	record.Set("processing_status", models.DocStatusPending)

	var options []models.FieldWrite
	if preserve != nil {
		options = preserve(record)
		worker.SetCreateSteps(record, models.ImportPreserveSteps)
	}

	return app.RunInTransaction(func(txApp core.App) error {
		if err := duplicates.NormalizeSaveError(txApp, record, txApp.Save(record)); err != nil {
			return err
		}
		return models.SaveFieldValues(txApp, record, options)
	})
}

func documentFilename(doc ngxDocument, file downloadedFile) string {
	filename := strings.TrimSpace(doc.OriginalFileName)
	if filename == "" {
		filename = strings.TrimSpace(doc.ArchivedFileName)
	}
	if filename == "" {
		filename = file.Name
	}
	filename = pathBase(filename)
	if filename == "" {
		filename = fmt.Sprintf("document-%d.bin", doc.ID)
	}
	return filename
}

func rejectKnownChecksum(app core.App, ownerUserID string, data []byte) error {
	checksum, err := duplicates.SHA256Reader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("hash file: %w", err)
	}
	if existing, err := duplicates.FindByChecksum(app, ownerUserID, checksum, ""); err != nil {
		return err
	} else if existing != nil {
		return &duplicates.ErrDuplicate{
			ExistingID:    existing.Id,
			ExistingTitle: existing.GetString("title"),
		}
	}
	return nil
}

// applyPreservedMetadata returns the options to save once the document exists.
func applyPreservedMetadata(record *core.Record, doc ngxDocument, tagMap, corrMap, typeMap map[int]string) []models.FieldWrite {
	var options []models.FieldWrite
	if title := strings.TrimSpace(doc.Title); title != "" {
		record.Set("title", title)
	}
	if ocr := strings.TrimSpace(doc.Content); ocr != "" {
		record.Set("ocr_text", ocr)
	}
	if date := documentDate(doc); date != "" {
		record.Set("document_date", date)
	}
	if doc.Correspondent != nil {
		if id := corrMap[*doc.Correspondent]; id != "" {
			options = append(options, models.FieldWrite{Field: models.CorrespondentField, Value: id})
		}
	}
	if doc.DocumentType != nil {
		if id := typeMap[*doc.DocumentType]; id != "" {
			options = append(options, models.FieldWrite{Field: models.DocumentTypeField, Value: id})
		}
	}
	if len(doc.Tags) > 0 {
		tagIDs := make([]string, 0, len(doc.Tags))
		for _, ngxTagID := range doc.Tags {
			if id := tagMap[ngxTagID]; id != "" {
				tagIDs = append(tagIDs, id)
			}
		}
		if len(tagIDs) > 0 {
			record.Set("tags", tagIDs)
		}
	}
	return options
}

func documentDate(doc ngxDocument) string {
	if d := strings.TrimSpace(doc.CreatedDate); len(d) >= 10 {
		return d[:10]
	}
	created := strings.TrimSpace(doc.Created)
	if created == "" {
		return ""
	}
	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05.999999Z",
		"2006-01-02 15:04:05.000Z",
		"2006-01-02",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, created); err == nil {
			return t.Format("2006-01-02")
		}
	}
	if len(created) >= 10 {
		return created[:10]
	}
	return ""
}

func pathBase(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSpace(name)
}

// count tallies one document's outcome; label names it in the error list.
func (r *Result) count(label string, err error) {
	if err == nil {
		r.Imported++
		return
	}
	if _, ok := errors.AsType[*duplicates.ErrDuplicate](err); ok {
		r.SkippedDuplicates++
		return
	}
	r.Failed++
	appendError(r, fmt.Sprintf("%s: %v", label, err))
}

func appendError(result *Result, msg string) {
	if len(result.Errors) >= maxReportedErrors {
		return
	}
	result.Errors = append(result.Errors, msg)
}
