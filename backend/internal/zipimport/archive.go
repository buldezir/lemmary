// Package zipimport imports documents out of a zip archive.
//
// Two sources use it and differ in one thing only, which entries count as
// documents; see Source. An Amazon "Request your data / Your Orders" export is
// a zip of CSV reports, delivery photos and — under
// Additional Data/Retail.TransactionalInvoicing.* — the invoice PDFs, so that
// flow takes the PDFs and ignores the rest. A zip the user packed themselves
// is all documents, so that one takes every type the collection can store.
//
// Either way the archive is staged on disk first, so the user can confirm what
// it holds before any document is created.
package zipimport

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/duplicates"
)

const (
	// maxEntries bounds one import run; each imported file also queues a
	// processing job.
	maxEntries = 5000
)

// maxEntryBytes matches the documents.file field limit, so an entry that cannot
// be stored is reported at preview time instead of failing mid-import.
// A var so tests can shrink it instead of building 20 MB fixtures.
var maxEntryBytes = DefaultMaxEntryBytes

// maxTotalScanBytes budgets the total decompression one scan may do. Real
// exports inflate to roughly the (already compressed) archive size; a crafted
// high-ratio zip could otherwise force ~maxEntries*maxEntryBytes (100 GB) of
// synchronous inflation inside one request. A var so tests can shrink it.
var maxTotalScanBytes int64 = 8 << 30

var (
	// ErrNotArchive is returned when the upload is not a readable zip.
	ErrNotArchive = errors.New("the upload is not a readable zip archive")
	// ErrNoFiles is returned when the archive holds nothing this source imports.
	// What that means depends on the Source; the caller words it for the user.
	ErrNoFiles = errors.New("no importable files found in the archive")
	// ErrTooManyFiles is returned when the archive exceeds maxEntries.
	ErrTooManyFiles = fmt.Errorf("the archive holds more than %d importable files", maxEntries)
	// ErrArchiveTooLarge is returned when the upload exceeds the staging limit.
	ErrArchiveTooLarge = errors.New("the archive is larger than this instance allows")
	// ErrArchiveTooDense is returned when the archive decompresses far beyond
	// any realistic export — the signature of a zip bomb.
	ErrArchiveTooDense = errors.New("the archive decompresses beyond the allowed total size")
)

// Source is what an import run counts as a document. An Amazon export is mostly
// CSV reports and delivery photos, so that flow stays PDF-only; a zip the user
// packed themselves takes everything the documents collection can store.
type Source string

const (
	SourceAmazon Source = "amazon"
	SourceFiles  Source = "files"
)

// storable is the documents.file MIME allowlist (migrations/1730000004)
// expressed as extensions. It is a pre-filter, not the gate: PocketBase decides
// by sniffing the content on save, so an entry that lies about its extension is
// still refused there.
//
// ponytail: extension-only, so the preview cannot promise what the save will
// accept -- a mislabelled entry passes the scan and lands in the run's error
// list instead. The fix is to sniff the first 4096 bytes in hashEntry with the
// same mimetype call PocketBase makes and flag the entry at preview time; do it
// the first time a real archive reports files failing on type.
var storable = map[string]bool{
	".pdf":  true,
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".txt":  true,
	".csv":  true,
	".docx": true,
	".xlsx": true,
}

// accepts reports whether this entry is a document for this source.
func (s Source) accepts(f *zip.File) bool {
	if f.FileInfo().IsDir() || isJunkEntry(f.Name) {
		return false
	}
	// An empty entry cannot become a document: filesystem.NewFileFromBytes
	// refuses zero bytes, so it would fail mid-import rather than at preview.
	if f.UncompressedSize64 == 0 {
		return false
	}
	ext := strings.ToLower(path.Ext(f.Name))
	if s == SourceAmazon {
		return ext == ".pdf"
	}
	return storable[ext]
}

// Entry is one importable file found in the archive.
type Entry struct {
	// Path is the entry path inside the zip; it identifies the entry on import.
	Path string `json:"path"`
	// Name is the file name the imported document gets.
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Duplicate is true when the file is already in the library, or when an
	// identical file appears earlier in the same archive.
	Duplicate bool `json:"duplicate"`
	// DuplicateOf is the existing document id, empty for an in-archive duplicate.
	DuplicateOf string `json:"duplicate_of,omitempty"`
	// Oversized is true when the file exceeds the per-document size limit.
	Oversized bool `json:"oversized"`

	checksum string
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

// scan walks the archive and describes every entry this source imports, marking
// the ones that already exist in the library. It also returns how many entries
// were ignored. Entries are hashed, so identical files inside one archive are
// only imported once.
func scan(src Source, lookup duplicateLookup, zr *zip.Reader) (entries []Entry, ignored int, err error) {
	seen := map[string]struct{}{}
	var totalBytes int64

	for _, f := range zr.File {
		if !src.accepts(f) {
			if !f.FileInfo().IsDir() && !isJunkEntry(f.Name) {
				ignored++
			}
			continue
		}
		if len(entries) >= maxEntries {
			return nil, 0, ErrTooManyFiles
		}

		entry := Entry{Path: f.Name, Name: documentName(f.Name)}

		// The zip header size is only a hint; hashing measures the real stream.
		checksum, size, err := hashEntry(f)
		entry.Size = size
		totalBytes += size
		if totalBytes > maxTotalScanBytes {
			return nil, 0, ErrArchiveTooDense
		}
		if errors.Is(err, errEntryTooLarge) {
			entry.Oversized = true
			entries = append(entries, entry)
			continue
		}
		if err != nil {
			return nil, 0, fmt.Errorf("read %s: %w", f.Name, err)
		}
		entry.checksum = checksum
		if _, repeated := seen[entry.checksum]; repeated {
			entry.Duplicate = true
		} else {
			seen[entry.checksum] = struct{}{}
			existingID, findErr := lookup(entry.checksum)
			if findErr != nil {
				return nil, 0, findErr
			}
			if existingID != "" {
				entry.Duplicate = true
				entry.DuplicateOf = existingID
			}
		}
		entries = append(entries, entry)
	}

	if len(entries) == 0 {
		return nil, ignored, ErrNoFiles
	}
	return entries, ignored, nil
}

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

// readEntry returns the bytes of one archive entry, bounded by maxEntryBytes.
func readEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, maxEntryBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxEntryBytes {
		return nil, errEntryTooLarge
	}
	return data, nil
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

// isJunkEntry filters archiver bookkeeping (macOS resource forks, AppleDouble
// side files) that would otherwise look like real entries.
func isJunkEntry(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "__MACOSX/") || strings.Contains(name, "/__MACOSX/") {
		return true
	}
	return strings.HasPrefix(path.Base(name), "._")
}

// documentName builds a readable file name from the archive path. The parent
// folder is kept as a prefix: Amazon numbers its invoices per folder (1.pdf,
// 2.pdf, ...) so it is the only thing telling them apart, and a zip somebody
// packed themselves keeps the folder context they chose to put the file in.
func documentName(entryPath string) string {
	entryPath = strings.ReplaceAll(entryPath, "\\", "/")
	base := path.Base(entryPath)
	parent := path.Base(path.Dir(entryPath))
	if parent == "." || parent == "/" || parent == "" || parent == base {
		return base
	}
	return parent + "-" + base
}
