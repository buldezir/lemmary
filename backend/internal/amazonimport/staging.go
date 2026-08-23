package amazonimport

import (
	"archive/zip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// stagingTTL is how long an uploaded archive waits for confirmation before it
// is swept. The user only has to answer one question, so this is generous.
const stagingTTL = 30 * time.Minute

// Preview describes a staged archive and is what the confirmation step renders.
type Preview struct {
	UploadID  string    `json:"upload_id"`
	FileName  string    `json:"file_name"`
	ExpiresAt time.Time `json:"expires_at"`
	// PDFCount is every PDF in the archive, duplicates included.
	PDFCount int `json:"pdf_count"`
	// ImportableCount is how many of those would become new documents.
	ImportableCount int `json:"importable_count"`
	DuplicateCount  int `json:"duplicate_count"`
	OversizedCount  int `json:"oversized_count"`
	// IgnoredCount is the non-PDF entries (CSV reports, delivery photos) skipped.
	IgnoredCount int     `json:"ignored_count"`
	Files        []Entry `json:"files"`
}

type stagedArchive struct {
	id          string
	ownerUserID string
	path        string
	expiresAt   time.Time
	preview     Preview
}

var (
	stagingMu sync.Mutex
	// stagingItems holds archives awaiting confirmation, keyed by upload id.
	stagingItems = map[string]*stagedArchive{}
	// stagingBusy holds the file names of archives currently being imported, so
	// a long import is never swept out from under itself.
	stagingBusy = map[string]struct{}{}
)

// Inspect stages the uploaded archive on disk and describes the PDFs it holds.
// Nothing is imported until Start is called with the returned upload id.
func Inspect(app core.App, ownerUserID, fileName string, src io.Reader) (Preview, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return Preview{}, fmt.Errorf("owner user id is required")
	}

	dir := stagingDir(app)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Preview{}, fmt.Errorf("prepare staging dir: %w", err)
	}
	sweepStaging(dir, time.Now())

	id, err := newUploadID()
	if err != nil {
		return Preview{}, err
	}
	archivePath := filepath.Join(dir, id+".zip")

	size, err := saveArchive(archivePath, src, MaxArchiveBytes)
	if err != nil {
		os.Remove(archivePath)
		return Preview{}, err
	}

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		os.Remove(archivePath)
		return Preview{}, ErrNotArchive
	}
	entries, ignored, err := scanPDFs(documentLookup(app, ownerUserID), &zr.Reader)
	zr.Close()
	if err != nil {
		os.Remove(archivePath)
		return Preview{}, err
	}

	item := &stagedArchive{
		id:          id,
		ownerUserID: ownerUserID,
		path:        archivePath,
		expiresAt:   time.Now().UTC().Add(stagingTTL),
		preview:     buildPreview(id, fileName, entries, ignored),
	}
	item.preview.ExpiresAt = item.expiresAt

	stagingMu.Lock()
	stagingItems[id] = item
	stagingMu.Unlock()

	app.Logger().Info("amazon archive staged",
		"component", "amazon_import",
		"upload_id", id,
		"bytes", size,
		"pdfs", item.preview.PDFCount,
		"importable", item.preview.ImportableCount,
	)
	return item.preview, nil
}

func buildPreview(uploadID, fileName string, entries []Entry, ignored int) Preview {
	preview := Preview{
		UploadID:     uploadID,
		FileName:     strings.TrimSpace(fileName),
		PDFCount:     len(entries),
		IgnoredCount: ignored,
		Files:        entries,
	}
	for _, entry := range entries {
		switch {
		case entry.Oversized:
			preview.OversizedCount++
		case entry.Duplicate:
			preview.DuplicateCount++
		default:
			preview.ImportableCount++
		}
	}
	return preview
}

// saveArchive streams src to path, refusing anything over limit bytes.
func saveArchive(path string, src io.Reader, limit int64) (int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create staging file: %w", err)
	}
	size, err := io.Copy(file, io.LimitReader(src, limit+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, fmt.Errorf("save upload: %w", err)
	}
	if size > limit {
		return 0, ErrArchiveTooLarge
	}
	if size == 0 {
		return 0, ErrNotArchive
	}
	return size, nil
}

// Discard drops a staged archive that the user chose not to import.
func Discard(uploadID, ownerUserID string) bool {
	item, ok := claimStaged(uploadID, ownerUserID)
	if !ok {
		return false
	}
	releaseStaged(item)
	return true
}

// claimStaged removes the archive from the registry and returns it, so the same
// upload cannot be imported (or discarded) twice.
func claimStaged(uploadID, ownerUserID string) (*stagedArchive, bool) {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	item, ok := stagingItems[uploadID]
	if !ok || item.ownerUserID != ownerUserID || time.Now().UTC().After(item.expiresAt) {
		return nil, false
	}
	delete(stagingItems, uploadID)
	stagingBusy[filepath.Base(item.path)] = struct{}{}
	return item, true
}

// releaseStaged deletes a claimed archive once the import (or discard) is done.
func releaseStaged(item *stagedArchive) {
	os.Remove(item.path)
	stagingMu.Lock()
	delete(stagingBusy, filepath.Base(item.path))
	stagingMu.Unlock()
}

// restoreStaged puts a claimed archive back for a later attempt.
func restoreStaged(item *stagedArchive) {
	stagingMu.Lock()
	delete(stagingBusy, filepath.Base(item.path))
	stagingItems[item.id] = item
	stagingMu.Unlock()
}

// sweepStaging drops expired registry entries and any archive file left behind
// by an earlier process (the registry does not survive a restart).
func sweepStaging(dir string, now time.Time) {
	stagingMu.Lock()
	live := make(map[string]struct{}, len(stagingItems)+len(stagingBusy))
	for name := range stagingBusy {
		live[name] = struct{}{}
	}
	for id, item := range stagingItems {
		if now.UTC().After(item.expiresAt) {
			delete(stagingItems, id)
			os.Remove(item.path)
			continue
		}
		live[filepath.Base(item.path)] = struct{}{}
	}
	stagingMu.Unlock()

	files, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if _, ok := live[file.Name()]; ok {
			continue
		}
		info, err := file.Info()
		if err != nil || now.Sub(info.ModTime()) <= stagingTTL {
			continue
		}
		os.Remove(filepath.Join(dir, file.Name()))
	}
}

func stagingDir(app core.App) string {
	return filepath.Join(app.DataDir(), "temp", "amazon_import")
}

func newUploadID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate upload id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
