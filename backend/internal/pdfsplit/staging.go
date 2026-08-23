package pdfsplit

import (
	"context"
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
	"paperless-go/backend/internal/pdftool"
)

// stagingTTL is how long a staged PDF waits for confirmation before it is
// swept. Marking the cuts by hand takes a while, so this is generous.
const stagingTTL = 30 * time.Minute

// sourceFileName is the staged original inside an upload's directory.
const sourceFileName = "source.pdf"

// thumbEdge is the longest edge of a page thumbnail. It is deliberately larger
// than preview.MaxEdge: a document card only has to be recognizable, while
// deciding where one document ends means actually reading the letterhead and
// heading of a page.
const thumbEdge = 900

// Preview describes a staged PDF and is what the cut-marking step renders.
type Preview struct {
	UploadID  string    `json:"upload_id"`
	FileName  string    `json:"file_name"`
	PageCount int       `json:"page_count"`
	SizeBytes int64     `json:"size_bytes"`
	ExpiresAt time.Time `json:"expires_at"`
}

type stagedPDF struct {
	id          string
	ownerUserID string
	dir         string
	expiresAt   time.Time
	preview     Preview
}

func (s *stagedPDF) sourcePath() string {
	return filepath.Join(s.dir, sourceFileName)
}

var (
	stagingMu sync.Mutex
	// stagingItems holds uploads awaiting confirmation, keyed by upload id.
	stagingItems = map[string]*stagedPDF{}
	// stagingBusy holds the directory names of uploads currently being split,
	// so a long split is never swept out from under itself.
	stagingBusy = map[string]struct{}{}
)

// Inspect stages the uploaded PDF, renders a thumbnail per page and reports the
// page count. Nothing is created until Start is called with the upload id.
func Inspect(app core.App, ownerUserID, fileName string, src io.Reader) (Preview, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return Preview{}, fmt.Errorf("owner user id is required")
	}

	root := stagingRoot(app)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Preview{}, fmt.Errorf("prepare staging dir: %w", err)
	}
	sweepStaging(root, time.Now())

	id, err := newUploadID()
	if err != nil {
		return Preview{}, err
	}
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Preview{}, fmt.Errorf("prepare upload dir: %w", err)
	}

	item := &stagedPDF{id: id, ownerUserID: ownerUserID, dir: dir}
	pageCount, size, err := savePDF(item.sourcePath(), src)
	if err != nil {
		os.RemoveAll(dir)
		return Preview{}, err
	}

	// Rendered up front, in one pdftoppm run: the cut-marking UI needs every
	// page at once, and re-rendering per request would pay the document parse
	// cost again for each thumbnail.
	if _, err := pdftool.RenderPages(context.Background(), item.sourcePath(), dir, thumbEdge, pageCount); err != nil {
		os.RemoveAll(dir)
		return Preview{}, fmt.Errorf("render page thumbnails: %w", err)
	}

	item.expiresAt = time.Now().UTC().Add(stagingTTL)
	item.preview = Preview{
		UploadID:  id,
		FileName:  strings.TrimSpace(fileName),
		PageCount: pageCount,
		SizeBytes: size,
		ExpiresAt: item.expiresAt,
	}

	stagingMu.Lock()
	stagingItems[id] = item
	stagingMu.Unlock()

	app.Logger().Info("pdf staged for splitting",
		"component", "pdf_split",
		"upload_id", id,
		"bytes", size,
		"pages", pageCount,
	)
	return item.preview, nil
}

// savePDF streams src to path and validates it is a splittable PDF, returning
// its page count and size.
func savePDF(path string, src io.Reader) (int, int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, 0, fmt.Errorf("create staging file: %w", err)
	}
	size, err := io.Copy(file, io.LimitReader(src, MaxPDFBytes+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, 0, fmt.Errorf("save upload: %w", err)
	}
	if size > MaxPDFBytes {
		return 0, 0, ErrPDFTooLarge
	}

	// The declared content type is the client's word; the header and a
	// successful page count are the file's.
	if !hasPDFHeader(path) {
		return 0, 0, ErrNotPDF
	}
	pageCount, err := pdftool.PageCount(context.Background(), path)
	if err != nil {
		return 0, 0, ErrNotPDF
	}
	if pageCount == 1 {
		return 0, 0, ErrSinglePage
	}
	if pageCount > MaxPages {
		return 0, 0, ErrTooManyPages
	}
	return pageCount, size, nil
}

func hasPDFHeader(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()

	var header [5]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return false
	}
	return string(header[:]) == "%PDF-"
}

// Discard drops a staged upload that the user chose not to split.
func Discard(uploadID, ownerUserID string) bool {
	item, ok := claimStaged(uploadID, ownerUserID)
	if !ok {
		return false
	}
	releaseStaged(item)
	return true
}

// Lookup returns the preview of a staged upload without claiming it, so the
// thumbnail and detect endpoints can be called repeatedly.
func Lookup(uploadID, ownerUserID string) (Preview, string, bool) {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	item, ok := stagingItems[uploadID]
	if !ok || item.ownerUserID != ownerUserID || time.Now().UTC().After(item.expiresAt) {
		return Preview{}, "", false
	}
	return item.preview, item.sourcePath(), true
}

// ThumbPath resolves the cached PNG for a page of a staged upload.
func ThumbPath(uploadID, ownerUserID string, page int) (string, bool) {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	item, ok := stagingItems[uploadID]
	if !ok || item.ownerUserID != ownerUserID || time.Now().UTC().After(item.expiresAt) {
		return "", false
	}
	if page < 1 || page > item.preview.PageCount {
		return "", false
	}
	return pdftool.PagePNGPath(item.dir, page), true
}

// claimStaged removes the upload from the registry and returns it, so the same
// upload cannot be split (or discarded) twice.
func claimStaged(uploadID, ownerUserID string) (*stagedPDF, bool) {
	stagingMu.Lock()
	defer stagingMu.Unlock()
	item, ok := stagingItems[uploadID]
	if !ok || item.ownerUserID != ownerUserID || time.Now().UTC().After(item.expiresAt) {
		return nil, false
	}
	delete(stagingItems, uploadID)
	stagingBusy[filepath.Base(item.dir)] = struct{}{}
	return item, true
}

// releaseStaged deletes a claimed upload once the split (or discard) is done.
func releaseStaged(item *stagedPDF) {
	os.RemoveAll(item.dir)
	stagingMu.Lock()
	delete(stagingBusy, filepath.Base(item.dir))
	stagingMu.Unlock()
}

// restoreStaged puts a claimed upload back for a later attempt.
func restoreStaged(item *stagedPDF) {
	stagingMu.Lock()
	delete(stagingBusy, filepath.Base(item.dir))
	stagingItems[item.id] = item
	stagingMu.Unlock()
}

// sweepStaging drops expired registry entries and any upload directory left
// behind by an earlier process (the registry does not survive a restart).
func sweepStaging(root string, now time.Time) {
	stagingMu.Lock()
	live := make(map[string]struct{}, len(stagingItems)+len(stagingBusy))
	for name := range stagingBusy {
		live[name] = struct{}{}
	}
	for id, item := range stagingItems {
		if now.UTC().After(item.expiresAt) {
			delete(stagingItems, id)
			os.RemoveAll(item.dir)
			continue
		}
		live[filepath.Base(item.dir)] = struct{}{}
	}
	stagingMu.Unlock()

	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if _, ok := live[entry.Name()]; ok {
			continue
		}
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= stagingTTL {
			continue
		}
		os.RemoveAll(filepath.Join(root, entry.Name()))
	}
}

func stagingRoot(app core.App) string {
	return filepath.Join(app.DataDir(), "temp", "split_upload")
}

func newUploadID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate upload id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
