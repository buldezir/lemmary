package ngximport

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"paperless-go/backend/internal/duplicates"
	"paperless-go/backend/internal/models"
	"paperless-go/backend/internal/worker"
)

const maxReportedErrors = 25

// Import mode values accepted by the API.
const (
	ModePreserve  = "preserve"
	ModeReprocess = "reprocess"
)

var (
	importMu   sync.Mutex
	importBusy bool
)

// ErrImportInProgress is returned when another import is already running.
var ErrImportInProgress = errors.New("an import is already in progress")

// Result summarizes a completed import run.
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

// Run imports taxonomy and documents from a remote Paperless-ngx instance.
// Only one import may run at a time.
func Run(app core.App, ownerUserID, baseURL, apiKey, mode string) (Result, error) {
	return RunWithClient(app, ownerUserID, baseURL, apiKey, mode, nil)
}

// RunWithClient is like Run but accepts a prebuilt client (for tests).
func RunWithClient(app core.App, ownerUserID, baseURL, apiKey, mode string, client *Client) (Result, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return Result{}, fmt.Errorf("owner user id is required")
	}
	parsedMode, err := ParseMode(mode)
	if err != nil {
		return Result{}, err
	}

	importMu.Lock()
	if importBusy {
		importMu.Unlock()
		return Result{}, ErrImportInProgress
	}
	importBusy = true
	importMu.Unlock()
	defer func() {
		importMu.Lock()
		importBusy = false
		importMu.Unlock()
	}()

	if client == nil {
		client, err = NewClient(baseURL, apiKey, nil)
		if err != nil {
			return Result{}, err
		}
	}

	result := Result{Errors: []string{}}

	var tagMap, corrMap, typeMap map[int]string
	if parsedMode == ModePreserve {
		tagMap, result.TagsUpserted, err = importNamedEntities(app, "tags", client.ListTags)
		if err != nil {
			return result, fmt.Errorf("import tags: %w", err)
		}

		corrMap, result.CorrespondentsUpserted, err = importNamedEntities(app, "correspondents", client.ListCorrespondents)
		if err != nil {
			return result, fmt.Errorf("import correspondents: %w", err)
		}

		typeMap, result.DocumentTypesUpserted, err = importNamedEntities(app, "document_types", client.ListDocumentTypes)
		if err != nil {
			return result, fmt.Errorf("import document types: %w", err)
		}
	} else {
		tagMap, corrMap, typeMap = map[int]string{}, map[int]string{}, map[int]string{}
	}

	docs, err := client.ListDocuments()
	if err != nil {
		return result, fmt.Errorf("list documents: %w", err)
	}

	for _, doc := range docs {
		if err := importOneDocument(app, client, ownerUserID, parsedMode, doc, tagMap, corrMap, typeMap); err != nil {
			var dup *duplicates.ErrDuplicate
			if errors.As(err, &dup) {
				result.SkippedDuplicates++
				continue
			}
			result.Failed++
			appendError(&result, fmt.Sprintf("document %d (%s): %v", doc.ID, strings.TrimSpace(doc.Title), err))
			continue
		}
		result.Imported++
	}

	return result, nil
}

func importNamedEntities(app core.App, collection string, list func() ([]namedEntity, error)) (map[int]string, int, error) {
	entities, err := list()
	if err != nil {
		return nil, 0, err
	}
	idMap := make(map[int]string, len(entities))
	upserted := 0
	for _, entity := range entities {
		name := strings.TrimSpace(entity.Name)
		if entity.ID == 0 || name == "" {
			continue
		}
		localID, err := ensureNamed(app, collection, name)
		if err != nil {
			return nil, upserted, err
		}
		idMap[entity.ID] = localID
		upserted++
	}
	return idMap, upserted, nil
}

func ensureNamed(app core.App, collection, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}

	existing, err := app.FindRecordsByFilter(
		collection,
		"name = {:name}",
		"",
		1,
		0,
		map[string]any{"name": name},
	)
	if err != nil {
		return "", err
	}
	if len(existing) > 0 {
		return existing[0].Id, nil
	}

	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		return "", err
	}
	record := core.NewRecord(coll)
	record.Set("name", name)
	if collection == "correspondents" || collection == "document_types" {
		record.Set("name_original", name)
	}
	if err := app.Save(record); err != nil {
		existing, findErr := app.FindRecordsByFilter(
			collection,
			"name = {:name}",
			"",
			1,
			0,
			map[string]any{"name": name},
		)
		if findErr == nil && len(existing) > 0 {
			return existing[0].Id, nil
		}
		return "", err
	}
	return record.Id, nil
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

	checksum, err := duplicates.SHA256Reader(bytes.NewReader(file.Data))
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

	fsFile, err := filesystem.NewFileFromBytes(file.Data, filename)
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

	if mode == ModePreserve {
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
				record.Set("correspondent", id)
			}
		}
		if doc.DocumentType != nil {
			if id := typeMap[*doc.DocumentType]; id != "" {
				record.Set("document_type", id)
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
		worker.RegisterCreateStepsForChecksum(checksum, models.ImportPreserveSteps)
		defer worker.ClearCreateStepsForChecksum(checksum)
	}

	if err := app.Save(record); err != nil {
		var dup *duplicates.ErrDuplicate
		if errors.As(err, &dup) {
			return dup
		}
		if dup := duplicates.ErrDuplicateFromSaveConflict(app, record, err); dup != nil {
			return dup
		}
		if strings.Contains(err.Error(), "document already exists") {
			return &duplicates.ErrDuplicate{}
		}
		return err
	}
	return nil
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

func appendError(result *Result, msg string) {
	if len(result.Errors) >= maxReportedErrors {
		return
	}
	result.Errors = append(result.Errors, msg)
}
