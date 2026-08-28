package archiveimport

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/backup"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/importjob"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/worker"
)

// Job statuses for in-memory async restores.
const (
	JobStatusRunning   = importjob.StatusRunning
	JobStatusCompleted = importjob.StatusCompleted
	JobStatusFailed    = importjob.StatusFailed
)

// Restore modes accepted by the API.
const (
	// ModeRestore puts the library back as it was: metadata, OCR text,
	// thumbnails and taxonomy all come from the archive, and no OCR or LLM
	// call is made.
	ModeRestore = "restore"
	// ModeReprocess imports only the original files and runs the full pipeline
	// over them, as if each had just been uploaded.
	ModeReprocess = "reprocess"
)

// ErrImportInProgress is returned when another import is already running.
var ErrImportInProgress = importjob.ErrBusy

// ErrUploadNotFound is returned when the upload id is unknown, expired, or
// belongs to someone else.
var ErrUploadNotFound = errors.New("upload not found or expired")

// ParseMode validates a restore mode; empty defaults to restore.
func ParseMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ModeRestore:
		return ModeRestore, nil
	case ModeReprocess:
		return ModeReprocess, nil
	default:
		return "", fmt.Errorf("mode must be %q or %q", ModeRestore, ModeReprocess)
	}
}

// Result summarizes a completed restore run.
type Result struct {
	Imported               int      `json:"imported"`
	SkippedDuplicates      int      `json:"skipped_duplicates"`
	SkippedOversized       int      `json:"skipped_oversized"`
	Failed                 int      `json:"failed"`
	TagsUpserted           int      `json:"tags_upserted"`
	CorrespondentsUpserted int      `json:"correspondents_upserted"`
	DocumentTypesUpserted  int      `json:"document_types_upserted"`
	Errors                 []string `json:"errors"`
}

// Job is an in-memory restore run snapshot (lost on process restart).
type Job = importjob.Job[Result]

var registry = importjob.NewRegistry[Result](importjob.DefaultRetention)

// Start restores a staged archive in the background and returns the job id.
// A run that gets as far as importing something consumes the archive: the
// upload id is no longer valid afterwards.
// Only one import may run at a time per owner.
func Start(app core.App, ownerUserID, uploadID, mode string) (string, error) {
	parsedMode, err := ParseMode(mode)
	if err != nil {
		return "", err
	}

	item, ok := stagingRegistry.Claim(uploadID, ownerUserID)
	if !ok {
		return "", ErrUploadNotFound
	}

	jobID, err := registry.Start(ownerUserID, func(report func(done, total int)) (result Result, runErr error) {
		finished := false
		// Settled from a defer because a panic in the run unwinds straight past
		// here into the job registry's recover, and an archive nobody settles is
		// neither retryable nor sweepable.
		defer func() { settleUpload(item, result, runErr, finished) }()
		result, runErr = runImport(app, ownerUserID, parsedMode, item, report)
		finished = true
		return result, runErr
	})
	if err != nil {
		// The job never started, so put the archive back for another attempt.
		stagingRegistry.Restore(item)
		return "", err
	}
	return jobID, nil
}

// settleUpload ends the hold a restore job took on the staged archive. A run
// that failed before importing anything — a corrupt zip, a missing collection —
// leaves it staged so it can be retried without a re-upload.
func settleUpload(item *stagedArchive, result Result, runErr error, finished bool) {
	if !finished || (runErr != nil && result.Imported == 0) {
		stagingRegistry.Restore(item)
		return
	}
	stagingRegistry.Release(item)
}

// GetJob returns a copy of the in-memory job, or false if unknown.
func GetJob(id string) (Job, bool) {
	return registry.Get(id)
}

// restoredDocument records what a created document still needs once every
// document exists: its original timestamps, and the archive-relative id of the
// near-duplicate it pointed at.
type restoredDocument struct {
	NewID               string
	ExportedID          string
	DuplicateOfExported string
	Created             string
	Updated             string
}

func runImport(app core.App, ownerUserID, mode string, item *stagedArchive, report func(done, total int)) (Result, error) {
	result := Result{Errors: []string{}}

	zr, err := zip.OpenReader(item.Path)
	if err != nil {
		return result, ErrNotArchive
	}
	defer zr.Close()

	files := indexEntries(&zr.Reader)
	resolver := newTaxonomyResolver(app, ownerUserID, &result)

	if mode == ModeRestore {
		// Ahead of the documents so tags nothing references still land: they
		// exist only in the manifest, and no document would ever create them.
		if err := resolver.preload(item.Payload.Taxonomy); err != nil {
			return result, fmt.Errorf("restore taxonomy: %w", err)
		}
	}

	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return result, err
	}

	entries := item.Payload.Files
	total := len(entries)
	report(0, total)

	// A second budget for the restore pass. Inspection only inflated the
	// originals and the metadata sidecars; the OCR text and thumbnails are
	// opened here for the first time, and per-entry caps alone would let a
	// few thousand highly compressed sidecars add up to an archive that costs
	// far more to unpack than it does to upload.
	budget := &scanBudget{remaining: maxTotalScanBytes}

	restored := make([]restoredDocument, 0, total)
	for i, entry := range entries {
		if doc := applyEntry(app, collection, ownerUserID, mode, entry, files, resolver, budget, &result); doc != nil {
			restored = append(restored, *doc)
		}
		report(i+1, total)
	}

	applyFixups(app, restored, &result)
	return result, nil
}

// applyEntry restores one previewed entry and folds the outcome into result.
func applyEntry(
	app core.App,
	collection *core.Collection,
	ownerUserID, mode string,
	entry Entry,
	files map[string]*zip.File,
	resolver *taxonomyResolver,
	budget *scanBudget,
	result *Result,
) *restoredDocument {
	switch {
	case entry.Oversized:
		result.SkippedOversized++
		return nil
	case entry.Duplicate:
		// Already in the library, or repeated inside this archive.
		result.SkippedDuplicates++
		return nil
	case entry.Missing:
		result.Failed++
		result.Errors = importjob.AppendError(result.Errors, fmt.Sprintf("%s: missing from archive", entry.Path))
		return nil
	}

	doc, err := restoreOne(app, collection, ownerUserID, mode, entry, files, resolver, budget)
	if err != nil {
		var dup *duplicates.ErrDuplicate
		if errors.As(err, &dup) {
			result.SkippedDuplicates++
			return nil
		}
		result.Failed++
		result.Errors = importjob.AppendError(result.Errors, fmt.Sprintf("%s: %v", entry.Name, err))
		return nil
	}
	result.Imported++
	return doc
}

func restoreOne(
	app core.App,
	collection *core.Collection,
	ownerUserID, mode string,
	entry Entry,
	files map[string]*zip.File,
	resolver *taxonomyResolver,
	budget *scanBudget,
) (*restoredDocument, error) {
	data, err := budget.take(files, entry.Path, maxEntryBytes)
	if err != nil {
		return nil, fmt.Errorf("read from archive: %w", err)
	}
	fsFile, err := filesystem.NewFileFromBytes(data, entry.Name)
	if err != nil {
		return nil, fmt.Errorf("prepare file: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("user", ownerUserID)
	record.Set("file", fsFile)
	record.Set("processing_status", models.DocStatusPending)

	doc := restoredDocument{ExportedID: entry.DocumentID}

	// A restore only has something to restore when the archive carries this
	// document's metadata. Without a sidecar the entry is just a file, so it
	// takes the ordinary upload path -- which is also what a pre-manifest
	// "originals" archive holds.
	if mode == ModeRestore && entry.metadataPath != "" {
		meta, err := readMetadataBudgeted(files, entry.metadataPath, budget)
		if err != nil {
			return nil, err
		}
		if err := applyMetadata(record, meta, resolver); err != nil {
			return nil, err
		}
		doc.DuplicateOfExported = stringField(meta, "duplicate_of")
		doc.Created, _ = parseTimestamp(stringField(meta, "created"))
		doc.Updated, _ = parseTimestamp(stringField(meta, "updated"))

		if entry.ocrPath != "" {
			raw, err := budget.take(files, entry.ocrPath, maxSidecarBytes)
			if err != nil {
				return nil, err
			}
			if text := string(raw); strings.TrimSpace(text) != "" {
				record.Set("ocr_text", text)
			}
		}
		if previewFile := restorePreview(files, entry.previewPath, budget); previewFile != nil {
			record.Set("preview", previewFile)
		}

		// No processing job at all. Everything a pipeline would derive is
		// already here, and letting it run would send a document whose OCR text
		// was legitimately empty to the OCR provider -- the export omits the
		// sidecar for an empty field, and the OCR step only skips when the
		// field is non-empty. Its saves would also race the timestamp fixup.
		worker.SkipCreateJob(record)
	}

	if err := duplicates.NormalizeSaveError(app, record, app.Save(record)); err != nil {
		return nil, err
	}
	doc.NewID = record.Id
	return &doc, nil
}

// pngMagic is the PNG signature. The preview field only accepts image/png, and
// a sidecar that is not one would fail validation and take the whole document
// down with it — over a thumbnail the pipeline can regenerate.
var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// maxPreviewBytes matches the documents.preview field limit.
const maxPreviewBytes = 2 << 20

func restorePreview(files map[string]*zip.File, previewPath string, budget *scanBudget) *filesystem.File {
	if previewPath == "" {
		return nil
	}
	data, err := budget.take(files, previewPath, maxPreviewBytes)
	if err != nil || !bytes.HasPrefix(data, pngMagic) {
		return nil
	}
	previewFile, err := filesystem.NewFileFromBytes(data, "preview.png")
	if err != nil {
		return nil
	}
	return previewFile
}

// applyFixups writes back what app.Save cannot.
//
// created and updated are autodate columns: PocketBase stamps them on every
// save, so a restored document would otherwise be dated the moment of the
// restore and the library would come back in the wrong order. duplicate_of is
// remapped in the same statement rather than through a second save, which would
// bump updated straight back to now.
func applyFixups(app core.App, restored []restoredDocument, result *Result) {
	if len(restored) == 0 {
		return
	}

	idByExported := make(map[string]string, len(restored))
	for _, doc := range restored {
		if doc.ExportedID != "" {
			idByExported[doc.ExportedID] = doc.NewID
		}
	}

	for _, doc := range restored {
		assignments := make([]string, 0, 3)
		params := dbx.Params{"id": doc.NewID}
		if doc.Created != "" {
			assignments = append(assignments, "created = {:created}")
			params["created"] = doc.Created
		}
		if doc.Updated != "" {
			assignments = append(assignments, "updated = {:updated}")
			params["updated"] = doc.Updated
		}
		// Only when the original is in this archive too; a near-duplicate
		// pointing at a document that was not restored has no target here.
		if target := idByExported[doc.DuplicateOfExported]; target != "" && target != doc.NewID {
			assignments = append(assignments, "duplicate_of = {:duplicateOf}")
			params["duplicateOf"] = target
		}
		if len(assignments) == 0 {
			continue
		}

		query := "UPDATE documents SET " + strings.Join(assignments, ", ") + " WHERE id = {:id}"
		if _, err := app.DB().NewQuery(query).Bind(params).Execute(); err != nil {
			// The document itself is restored; only its dates are off.
			result.Errors = importjob.AppendError(result.Errors, fmt.Sprintf("%s: restore timestamps: %v", doc.NewID, err))
		}
	}
}

// taxonomyResolver maps a taxonomy name to this instance's record id, creating
// the record on first use and remembering it, so a library of a few hundred
// documents sharing a handful of tags does one lookup per tag rather than one
// per document.
type taxonomyResolver struct {
	app            core.App
	ownerUserID    string
	tags           map[string]string
	correspondents map[string]string
	documentTypes  map[string]string
	result         *Result
}

func newTaxonomyResolver(app core.App, ownerUserID string, result *Result) *taxonomyResolver {
	return &taxonomyResolver{
		app:            app,
		ownerUserID:    ownerUserID,
		tags:           map[string]string{},
		correspondents: map[string]string{},
		documentTypes:  map[string]string{},
		result:         result,
	}
}

// preload ensures every taxonomy record the manifest carries, including the
// ones no document references.
func (r *taxonomyResolver) preload(taxonomy backup.Taxonomy) error {
	for _, name := range taxonomy.Tags {
		if _, err := r.tag(name); err != nil {
			return err
		}
	}
	for _, entity := range taxonomy.Correspondents {
		if _, err := r.namedEntity("correspondents", entity.Name, entity.NameOriginal); err != nil {
			return err
		}
	}
	for _, entity := range taxonomy.DocumentTypes {
		if _, err := r.namedEntity("document_types", entity.Name, entity.NameOriginal); err != nil {
			return err
		}
	}
	return nil
}

func (r *taxonomyResolver) tag(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	if id, ok := r.tags[name]; ok {
		return id, nil
	}
	id, created, err := worker.EnsureTag(r.app, r.ownerUserID, name)
	if err != nil {
		return "", err
	}
	r.tags[name] = id
	if created {
		r.result.TagsUpserted++
	}
	return id, nil
}

func (r *taxonomyResolver) namedEntity(collection, name, originalName string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	cache := r.correspondents
	counter := &r.result.CorrespondentsUpserted
	if collection == "document_types" {
		cache = r.documentTypes
		counter = &r.result.DocumentTypesUpserted
	}
	if id, ok := cache[name]; ok {
		return id, nil
	}
	id, created, err := worker.EnsureNamedEntity(r.app, collection, r.ownerUserID, name, originalName)
	if err != nil {
		return "", err
	}
	cache[name] = id
	if created {
		*counter++
	}
	return id, nil
}
