// Package amazonimport imports the PDF invoices out of an Amazon
// "Request your data / Your Orders" export archive.
//
// The export is a zip of CSV reports, delivery photos and — under
// Additional Data/Retail.TransactionalInvoicing.* — the invoice PDFs. Only PDFs
// are imported; every other entry is ignored. The archive is staged on disk
// first so the user can confirm the file count before any document is created.
package amazonimport

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
	// maxPDFs bounds one import run; each imported PDF also queues a processing job.
	maxPDFs = 5000
)

// maxEntryBytes matches the documents.file field limit, so an entry that cannot
// be stored is reported at preview time instead of failing mid-import.
// A var so tests can shrink it instead of building 20 MB fixtures.
var maxEntryBytes int64 = 20 << 20

// maxTotalScanBytes budgets the total decompression one scan may do. Real
// exports inflate to roughly the (already compressed) archive size; a crafted
// high-ratio zip could otherwise force ~maxPDFs*maxEntryBytes (100 GB) of
// synchronous inflation inside one request. A var so tests can shrink it.
var maxTotalScanBytes int64 = 8 << 30

var (
	// ErrNotArchive is returned when the upload is not a readable zip.
	ErrNotArchive = errors.New("the upload is not a readable zip archive")
	// ErrNoPDFs is returned when the archive holds no PDF files.
	ErrNoPDFs = errors.New("no PDF files found in the archive")
	// ErrTooManyPDFs is returned when the archive exceeds maxPDFs.
	ErrTooManyPDFs = fmt.Errorf("the archive holds more than %d PDF files", maxPDFs)
	// ErrArchiveTooLarge is returned when the upload exceeds the staging limit.
	ErrArchiveTooLarge = errors.New("the archive is larger than this instance allows")
	// ErrArchiveTooDense is returned when the archive decompresses far beyond
	// any realistic export — the signature of a zip bomb.
	ErrArchiveTooDense = errors.New("the archive decompresses beyond the allowed total size")
)

// Entry is one PDF found in the archive.
type Entry struct {
	// Path is the entry path inside the zip; it identifies the entry on import.
	Path string `json:"path"`
	// Name is the file name the imported document gets.
	Name string `json:"name"`
	Size int64  `json:"size"`
	// Duplicate is true when the PDF is already in the library, or when an
	// identical PDF appears earlier in the same archive.
	Duplicate bool `json:"duplicate"`
	// DuplicateOf is the existing document id, empty for an in-archive duplicate.
	DuplicateOf string `json:"duplicate_of,omitempty"`
	// Oversized is true when the PDF exceeds the per-document size limit.
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

// scanPDFs walks the archive and describes every PDF entry, marking the ones
// that already exist in the library. It also returns how many non-PDF entries
// were ignored. Entries are hashed, so identical PDFs inside one archive are
// only imported once.
func scanPDFs(lookup duplicateLookup, zr *zip.Reader) (entries []Entry, ignored int, err error) {
	seen := map[string]struct{}{}
	var totalBytes int64

	for _, f := range zr.File {
		if !isPDFEntry(f) {
			if !f.FileInfo().IsDir() && !isJunkEntry(f.Name) {
				ignored++
			}
			continue
		}
		if len(entries) >= maxPDFs {
			return nil, 0, ErrTooManyPDFs
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
		return nil, ignored, ErrNoPDFs
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

func isPDFEntry(f *zip.File) bool {
	if f.FileInfo().IsDir() || isJunkEntry(f.Name) {
		return false
	}
	if f.UncompressedSize64 == 0 {
		return false
	}
	return strings.EqualFold(path.Ext(f.Name), ".pdf")
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

// documentName builds a readable file name from the archive path. Amazon numbers
// the invoices per folder (1.pdf, 2.pdf, ...), so the parent folder is kept as a
// prefix to tell the two invoicing folders apart.
func documentName(entryPath string) string {
	entryPath = strings.ReplaceAll(entryPath, "\\", "/")
	base := path.Base(entryPath)
	parent := path.Base(path.Dir(entryPath))
	if parent == "." || parent == "/" || parent == "" || parent == base {
		return base
	}
	return parent + "-" + base
}
