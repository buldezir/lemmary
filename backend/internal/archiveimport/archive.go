// Package archiveimport restores a Lemmary backup archive — the zip produced by
// GET /api/app/documents/export — back into a library.
//
// The archive is staged on disk first and described to the user, so a restore
// that would mostly be skipped duplicates is visible before anything is
// created. Nothing is written until the user confirms the upload id.
//
// See internal/backup for the archive layout this reads.
package archiveimport

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/backup"
	"lemmary/backend/internal/duplicates"
)

const (
	// maxDocuments bounds one restore. A reprocess also queues a pipeline job
	// per document, which is the expensive half.
	maxDocuments = 5000
)

// maxEntryBytes matches the documents.file field limit, so an entry that cannot
// be stored is reported at preview time instead of failing mid-restore.
// A var so tests can shrink it instead of building 20 MB fixtures.
var maxEntryBytes = DefaultMaxEntryBytes

// maxTotalScanBytes budgets the total decompression one scan may do, across
// both the originals it hashes and the sidecars it reads. A var so tests can
// shrink it.
var maxTotalScanBytes int64 = 8 << 30

var (
	// ErrNotArchive is returned when the upload is not a readable zip.
	ErrNotArchive = errors.New("the upload is not a readable Lemmary archive")
	// ErrNoDocuments is returned when the archive holds no restorable document.
	ErrNoDocuments = errors.New("no documents found in the archive")
	// ErrTooManyDocuments is returned when the archive exceeds maxDocuments.
	ErrTooManyDocuments = fmt.Errorf("the archive holds more than %d documents", maxDocuments)
	// ErrArchiveTooLarge is returned when the upload exceeds the staging limit.
	ErrArchiveTooLarge = errors.New("the archive is larger than this instance allows")
	// ErrArchiveTooDense is returned when the archive decompresses far beyond
	// any realistic backup — the signature of a zip bomb.
	ErrArchiveTooDense = errors.New("the archive decompresses beyond the allowed total size")
	// ErrUnsupportedVersion is returned for an archive from a newer Lemmary.
	ErrUnsupportedVersion = backup.ErrUnsupportedVersion
)

// Entry is one document found in the archive, as the preview reports it and as
// the restore consumes it.
type Entry struct {
	// DocumentID is the id the document had in the instance it was exported
	// from. It relates documents to each other (duplicate_of); the restored
	// record always gets a fresh id.
	DocumentID string `json:"document_id"`
	Title      string `json:"title"`
	// Path is the original file's entry path inside the zip.
	Path string `json:"path"`
	// Name is the file name the restored document gets.
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Duplicate is true when the file is already in the library, or when an
	// identical file appears earlier in the same archive.
	Duplicate bool `json:"duplicate"`
	// DuplicateOf is the existing document id, empty for an in-archive duplicate.
	DuplicateOf string `json:"duplicate_of,omitempty"`
	// Oversized is true when the file exceeds the per-document size limit.
	Oversized bool `json:"oversized"`
	// Missing is true when the manifest names a file the archive does not hold.
	Missing     bool `json:"missing"`
	HasOCR      bool `json:"has_ocr"`
	HasMetadata bool `json:"has_metadata"`
	HasPreview  bool `json:"has_preview"`

	checksum     string
	ocrPath      string
	metadataPath string
	previewPath  string
}

// duplicateLookup reports the id of an existing document with this checksum,
// or "" when the checksum is new.
type duplicateLookup func(checksum string) (string, error)

// documentLookup checks the owner's documents for an identical file.
func documentLookup(app core.App, ownerUserID string) duplicateLookup {
	return func(checksum string) (string, error) {
		existing, err := duplicates.FindByChecksum(app, ownerUserID, checksum, "")
		if err != nil || existing == nil {
			return "", err
		}
		return existing.Id, nil
	}
}

// scan describes every document the archive holds, marking the ones that
// already exist in the library, and returns the taxonomy to restore alongside
// them plus the number of entries that belong to no document.
func scan(lookup duplicateLookup, zr *zip.Reader, manifest *backup.Manifest) (entries []Entry, taxonomy backup.Taxonomy, ignored int, err error) {
	if manifest != nil {
		taxonomy = manifest.Taxonomy
	}

	groups, ignored := backup.Groups(zr, manifest)
	// A backup of a library with no documents is still a backup: its manifest
	// carries the taxonomy, which lives nowhere else in the archive. Rejecting
	// it would discard the only thing it holds.
	if len(groups) == 0 && taxonomy.Count() == 0 {
		return nil, taxonomy, ignored, ErrNoDocuments
	}
	if len(groups) > maxDocuments {
		return nil, taxonomy, 0, ErrTooManyDocuments
	}

	files := indexEntries(zr)
	seen := map[string]struct{}{}
	budget := &scanBudget{remaining: maxTotalScanBytes}

	entries = make([]Entry, 0, len(groups))
	for _, group := range groups {
		entry := Entry{
			DocumentID:   group.ID,
			Title:        group.Title,
			Path:         group.File,
			ocrPath:      group.OCR,
			metadataPath: group.Metadata,
			previewPath:  group.Preview,
			HasOCR:       group.OCR != "",
			HasMetadata:  group.Metadata != "",
			HasPreview:   group.Preview != "",
		}

		file := files[group.File]
		if file == nil {
			entry.Missing = true
			entry.Name = fallbackName(group)
			entries = append(entries, entry)
			continue
		}
		// Reading the sidecar to recover the upload name inflates archive data
		// too, so it draws on the same budget as the originals.
		entry.Name, err = restoredName(files, group, budget)
		if err != nil {
			return nil, taxonomy, 0, err
		}

		// The zip header size is only a hint; hashing measures the real stream.
		checksum, size, hashErr := hashEntry(file)
		entry.Size = size
		if !budget.spend(size) {
			return nil, taxonomy, 0, ErrArchiveTooDense
		}
		if errors.Is(hashErr, errEntryTooLarge) {
			entry.Oversized = true
			entries = append(entries, entry)
			continue
		}
		if hashErr != nil {
			return nil, taxonomy, 0, fmt.Errorf("read %s: %w", group.File, hashErr)
		}

		entry.checksum = checksum
		if _, repeated := seen[checksum]; repeated {
			entry.Duplicate = true
		} else {
			seen[checksum] = struct{}{}
			existingID, findErr := lookup(checksum)
			if findErr != nil {
				return nil, taxonomy, 0, findErr
			}
			if existingID != "" {
				entry.Duplicate = true
				entry.DuplicateOf = existingID
			}
		}
		entries = append(entries, entry)
	}

	return entries, taxonomy, ignored, nil
}

// restoredName is the file name the restored document gets: the name it was
// uploaded under, recovered from the metadata sidecar. Without a sidecar the
// entry name is all there is, so the "[id] " prefix is stripped off it.
func restoredName(files map[string]*zip.File, group backup.Group, budget *scanBudget) (string, error) {
	if group.Metadata == "" {
		return fallbackName(group), nil
	}
	// A sidecar that is unreadable or absurdly large is not worth failing the
	// archive over; the entry name still names the document. Running out of
	// budget is different -- that is the archive as a whole being abusive.
	data, err := budget.take(files, group.Metadata, maxSidecarBytes)
	if errors.Is(err, ErrArchiveTooDense) {
		return "", err
	}
	if err != nil {
		return fallbackName(group), nil
	}
	var meta map[string]any
	if json.Unmarshal(data, &meta) == nil {
		if name := cleanFileName(stringField(meta, "original_filename")); name != "" {
			return name, nil
		}
	}
	return fallbackName(group), nil
}

// scanBudget is the running remainder of maxTotalScanBytes. Without it a
// crafted high-ratio zip could force ~maxDocuments times the per-entry limit of
// synchronous inflation inside a single request.
type scanBudget struct{ remaining int64 }

// spend draws n bytes and reports whether the budget held.
func (b *scanBudget) spend(n int64) bool {
	b.remaining -= n
	return b.remaining >= 0
}

// fallbackName rebuilds a file name from the entry path alone.
func fallbackName(group backup.Group) string {
	base := path.Base(group.File)
	ext := path.Ext(base)
	if _, title, ok := backup.ParseEntryBase(strings.TrimSuffix(base, ext)); ok && title != "" {
		return title + ext
	}
	if name := cleanFileName(base); name != "" {
		return name
	}
	return "document" + ext
}

// cleanFileName reduces a stored name to a single safe path element: the
// metadata sidecar is data from another instance, so a "../" in it must not
// reach the filesystem.
func cleanFileName(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "\\", "/")
	name = path.Base(name)
	if name == "." || name == "/" || name == ".." {
		return ""
	}
	return strings.TrimSpace(name)
}

// indexEntries maps entry name to zip entry. Duplicate names are legal in a
// zip; first-wins keeps the scan and the restore reading the same bytes.
func indexEntries(zr *zip.Reader) map[string]*zip.File {
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || backup.IsJunkEntry(f.Name) {
			continue
		}
		if _, exists := files[f.Name]; exists {
			continue
		}
		files[f.Name] = f
	}
	return files
}

// maxSidecarBytes bounds an OCR or metadata sidecar. ocr_text is capped at
// 500k characters by the collection, so anything beyond this is not a sidecar
// this instance could have written.
const maxSidecarBytes = 4 << 20

// errEntryTooLarge marks an entry that cannot be stored as a document.
var errEntryTooLarge = errors.New("entry exceeds the per-document size limit")

// hashEntry returns the checksum and the real uncompressed size of an entry.
func hashEntry(f *zip.File) (string, int64, error) {
	rc, err := f.Open()
	if err != nil {
		return "", 0, err
	}
	defer rc.Close()

	counter := &countingReader{r: io.LimitReader(rc, maxEntryBytes+1)}
	checksum, err := duplicates.SHA256Reader(counter)
	if err != nil {
		return "", counter.n, err
	}
	if counter.n > maxEntryBytes {
		return "", counter.n, errEntryTooLarge
	}
	return checksum, counter.n, nil
}

// readEntry returns the bytes of one archive entry, bounded by limit.
func readEntry(f *zip.File, limit int64) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errEntryTooLarge
	}
	return data, nil
}

// take reads one entry and draws its real size from the budget.
//
// It is the only way an entry is read after inspection, so every byte the
// restore inflates is accounted for. The size is the length actually read, not
// the zip header's claim, which a crafted archive is free to understate.
func (b *scanBudget) take(files map[string]*zip.File, name string, limit int64) ([]byte, error) {
	file := files[name]
	if file == nil {
		return nil, fmt.Errorf("%s missing from archive", name)
	}
	data, err := readEntry(file, limit)
	if err != nil {
		return nil, err
	}
	if !b.spend(int64(len(data))) {
		return nil, ErrArchiveTooDense
	}
	return data, nil
}

// readMetadataBudgeted reads and parses one metadata sidecar.
func readMetadataBudgeted(files map[string]*zip.File, name string, budget *scanBudget) (map[string]any, error) {
	data, err := budget.take(files, name, maxSidecarBytes)
	if err != nil {
		return nil, err
	}
	var meta map[string]any
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, fmt.Errorf("parse metadata %s: %w", name, err)
	}
	return meta, nil
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}
