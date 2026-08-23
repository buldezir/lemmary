package pdfsplit

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/pdftool"
	"paperless-go/backend/internal/staging"
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

// staged is the payload of a registry entry: the preview plus the guard that
// keeps the lazy thumbnail renders of this upload from piling up.
type staged struct {
	preview Preview
	// renderMu serializes this upload's page renders, so two requests for the
	// same page do not run pdftoppm twice over the same file.
	renderMu sync.Mutex
}

// stagedPDF is one upload waiting to be split.
type stagedPDF = staging.Item[*staged]

var stagingRegistry = newStagingRegistry()

func newStagingRegistry() *staging.Registry[*staged] {
	return staging.New[*staged](staging.Config{
		TTL: stagingTTL,
		// An upload owns a directory: the source PDF plus the thumbnails
		// rendered from it.
		Remove:  os.RemoveAll,
		Manages: staging.Directories,
	})
}

func sourcePathOf(item *stagedPDF) string {
	return filepath.Join(item.Path, sourceFileName)
}

// Inspect stages the uploaded PDF and reports the page count. Nothing is
// created until Start is called with the upload id.
func Inspect(app core.App, ownerUserID, fileName string, src io.Reader) (Preview, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return Preview{}, fmt.Errorf("owner user id is required")
	}

	root := stagingRoot(app)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Preview{}, fmt.Errorf("prepare staging dir: %w", err)
	}
	stagingRegistry.Sweep(root, time.Now())

	id, err := staging.NewID()
	if err != nil {
		return Preview{}, err
	}
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Preview{}, fmt.Errorf("prepare upload dir: %w", err)
	}

	pageCount, size, err := savePDF(filepath.Join(dir, sourceFileName), src)
	if err != nil {
		os.RemoveAll(dir)
		return Preview{}, err
	}

	// Thumbnails are rendered on demand rather than here: a hundred pages at
	// thumbEdge takes minutes, and holding the upload request open for that
	// risks a client or proxy timeout while the cut-marking UI is perfectly
	// happy to paint pages as they arrive.
	expiresAt := time.Now().UTC().Add(stagingTTL)
	item := &stagedPDF{
		ID:          id,
		OwnerUserID: ownerUserID,
		Path:        dir,
		ExpiresAt:   expiresAt,
		Payload: &staged{preview: Preview{
			UploadID:  id,
			FileName:  strings.TrimSpace(fileName),
			PageCount: pageCount,
			SizeBytes: size,
			ExpiresAt: expiresAt,
		}},
	}
	stagingRegistry.Add(item)

	app.Logger().Info("pdf staged for splitting",
		"component", "pdf_split",
		"upload_id", id,
		"bytes", size,
		"pages", pageCount,
	)
	return item.Payload.preview, nil
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
	item, ok := stagingRegistry.Claim(uploadID, ownerUserID)
	if !ok {
		return false
	}
	stagingRegistry.Release(item)
	return true
}

// Lookup returns the preview of a staged upload without claiming it, so the
// thumbnail and detect endpoints can be called repeatedly.
func Lookup(uploadID, ownerUserID string) (Preview, string, bool) {
	item, ok := stagingRegistry.Lookup(uploadID, ownerUserID)
	if !ok {
		return Preview{}, "", false
	}
	return item.Payload.preview, sourcePathOf(item), true
}

// PageThumb returns the PNG of one page of a staged upload, rendering it on
// first request and reusing the cached file afterwards.
//
// The bytes are read while the upload is held, so a discard or a confirmed
// split running at the same time cannot delete the directory underneath.
func PageThumb(uploadID, ownerUserID string, page int) ([]byte, error) {
	item, ok := stagingRegistry.Hold(uploadID, ownerUserID)
	if !ok {
		return nil, ErrUploadNotFound
	}
	defer stagingRegistry.Unhold(item)

	if page < 1 || page > item.Payload.preview.PageCount {
		return nil, ErrUploadNotFound
	}
	path := pdftool.PagePNGPath(item.Path, page)
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	}

	// Serialized per upload: the UI fetches a few pages at a time, and letting
	// each of those start its own poppler process over the same file buys
	// little for the load it adds.
	item.Payload.renderMu.Lock()
	defer item.Payload.renderMu.Unlock()
	if data, err := os.ReadFile(path); err == nil {
		return data, nil
	}
	if err := pdftool.RenderPage(context.Background(), sourcePathOf(item), path, thumbEdge, page); err != nil {
		return nil, fmt.Errorf("render page %d: %w", page, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read rendered page %d: %w", page, err)
	}
	return data, nil
}

func stagingRoot(app core.App) string {
	return filepath.Join(app.DataDir(), "temp", "split_upload")
}
