package escl

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/importjob"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/pdftool"
	"lemmary/backend/internal/staging"
)

// A scan is staged rather than saved a page at a time, because the thing the
// user is making is one document out of several sheets of glass. The staged
// file is the document so far; each scan merges onto the end of it, and saving
// hands the whole thing to the normal upload path.
const (
	// stagingTTL is how long a half-scanned document waits. Feeding sheets by
	// hand takes a while, so this matches the PDF split's allowance.
	stagingTTL = 30 * time.Minute

	// maxScanBytes is the documents.file field's own MaxSize (see
	// migrations/1730000001_initial.go). Enforcing it here means a scan that
	// cannot be stored is refused while there is still something to do about it
	// -- save what you have and start a second document -- rather than at the
	// end, after the pages are gone.
	maxScanBytes int64 = 20 << 20
)

var (
	// ErrScanInProgress is returned when the owner already has a scan running.
	ErrScanInProgress = importjob.ErrBusy
	// ErrUploadNotFound is returned for an unknown, expired or foreign scan.
	ErrUploadNotFound = errors.New("scan not found")
	// ErrTooLarge is returned when the document so far has no room for more.
	ErrTooLarge = fmt.Errorf("the scanned document is at the %d MB limit; save it and start another", maxScanBytes>>20)
)

// Job statuses for the in-memory scan job.
const (
	JobStatusRunning   = importjob.StatusRunning
	JobStatusCompleted = importjob.StatusCompleted
	JobStatusFailed    = importjob.StatusFailed
)

// Preview is the staged document as the Scan page shows it.
type Preview struct {
	UploadID  string    `json:"upload_id"`
	PageCount int       `json:"page_count"`
	SizeBytes int64     `json:"size_bytes"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Job is one scan run (lost on process restart, like every other import job).
type Job = importjob.Job[Preview]

type staged struct{ preview Preview }

type stagedScan = staging.Item[*staged]

var stagingRegistry = newStagingRegistry()

func newStagingRegistry() *staging.Registry[*staged] {
	return staging.New[*staged](staging.Config{
		TTL: stagingTTL,
		// One upload owns one file: the document scanned so far.
		Remove:  os.Remove,
		Manages: staging.Files,
	})
}

var registry = importjob.NewRegistry[Preview](importjob.DefaultRetention)

// GetJob returns a copy of the in-memory job, or false if unknown.
func GetJob(id string) (Job, bool) { return registry.Get(id) }

func stagingRoot(app core.App) string {
	return filepath.Join(app.DataDir(), "temp", "scan")
}

// Start scans in the background and returns the job id.
//
// An empty uploadID begins a new document; otherwise the pages are appended to
// that one. Only one scan runs at a time per owner, which is also what keeps
// two runs from merging onto the same staged file.
func Start(app core.App, ownerUserID, scanner string, source Source, uploadID string) (string, error) {
	if _, err := normalizeBase(scanner); err != nil {
		return "", err
	}

	var item *stagedScan
	if uploadID != "" {
		claimed, ok := stagingRegistry.Claim(uploadID, ownerUserID)
		if !ok {
			// A scan already running for this owner has the upload claimed, so
			// the lookup fails for a reason the user can act on: the pages are
			// fine, the previous scan is simply still going.
			if registry.Busy(ownerUserID) {
				return "", ErrScanInProgress
			}
			return "", ErrUploadNotFound
		}
		if claimed.Payload.preview.SizeBytes >= maxScanBytes {
			stagingRegistry.Restore(claimed)
			return "", ErrTooLarge
		}
		item = claimed
	}

	jobID, err := registry.Start(ownerUserID, func(func(done, total int)) (Preview, error) {
		// From a defer because a panic in the run unwinds straight into the job
		// registry's recover, and a claimed upload nobody puts back is neither
		// reachable nor sweepable.
		if item != nil {
			defer stagingRegistry.Restore(item)
		}
		return runScan(app, ownerUserID, scanner, source, item)
	})
	if err != nil {
		if item != nil {
			stagingRegistry.Restore(item)
		}
		return "", err
	}
	return jobID, nil
}

// runScan performs one scan and folds its pages into the staged document,
// creating that document when this is the first scan.
func runScan(app core.App, ownerUserID, scanner string, source Source, item *stagedScan) (Preview, error) {
	budget := maxScanBytes
	if item != nil {
		budget -= item.Payload.preview.SizeBytes
	}

	pages, err := Scan(context.Background(), scanner, source, budget)
	if err != nil {
		return Preview{}, err
	}

	workDir, err := os.MkdirTemp("", "lemmary-scan-*")
	if err != nil {
		return Preview{}, fmt.Errorf("prepare work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	paths := make([]string, 0, len(pages))
	for i, page := range pages {
		path := filepath.Join(workDir, fmt.Sprintf("page-%d.pdf", i+1))
		if err := os.WriteFile(path, page, 0o600); err != nil {
			return Preview{}, fmt.Errorf("write scanned page: %w", err)
		}
		paths = append(paths, path)
	}

	fresh := item == nil
	if fresh {
		item, err = stage(app, ownerUserID, paths)
		if err != nil {
			return Preview{}, err
		}
	} else if err := merge(item.Path, paths); err != nil {
		return Preview{}, err
	}

	preview, err := describe(item)
	if err != nil {
		if fresh {
			// Never registered, so nothing else will ever clean it up.
			os.Remove(item.Path)
		}
		return Preview{}, err
	}
	item.Payload.preview = preview
	// The wait starts again from the page just scanned: feeding a long document
	// by hand takes longer than one TTL, and expiring mid-document would throw
	// away every page already through the glass. Safe to write here because a
	// claimed item is out of the registry, and a fresh one is not in it yet.
	item.ExpiresAt = time.Now().UTC().Add(stagingTTL)
	if fresh {
		// Published only once the payload is filled in, so nothing can resolve
		// a scan whose preview is still zero.
		stagingRegistry.Add(item)
	}

	app.Logger().Info("scan added pages",
		"component", "scan",
		"upload_id", item.ID,
		"scanned", len(pages),
		"pages", preview.PageCount,
		"bytes", preview.SizeBytes,
	)
	return preview, nil
}

// stage creates the staged document from the first scan's pages.
func stage(app core.App, ownerUserID string, paths []string) (*stagedScan, error) {
	root := stagingRoot(app)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("prepare staging dir: %w", err)
	}
	stagingRegistry.Sweep(root, time.Now())

	id, err := staging.NewID()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(root, id+".pdf")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return nil, fmt.Errorf("prepare staged scan: %w", err)
	}
	if err := merge(path, paths); err != nil {
		os.Remove(path)
		return nil, err
	}

	return &stagedScan{
		ID:          id,
		OwnerUserID: ownerUserID,
		Path:        path,
		ExpiresAt:   time.Now().UTC().Add(stagingTTL),
		Payload:     &staged{},
	}, nil
}

// merge appends pages to the staged document. The staged file is empty on the
// first scan, and pdfunite has nothing to do with an empty input, so the first
// page is copied into place instead.
func merge(stagedPath string, paths []string) error {
	info, err := os.Stat(stagedPath)
	if err != nil {
		return fmt.Errorf("read staged scan: %w", err)
	}

	inputs := paths
	if info.Size() > 0 {
		inputs = append([]string{stagedPath}, paths...)
	}
	if len(inputs) == 1 {
		data, err := os.ReadFile(inputs[0])
		if err != nil {
			return fmt.Errorf("read scanned page: %w", err)
		}
		if err := os.WriteFile(stagedPath, data, 0o600); err != nil {
			return fmt.Errorf("write staged scan: %w", err)
		}
		return nil
	}
	return pdftool.Merge(context.Background(), stagedPath, inputs...)
}

func describe(item *stagedScan) (Preview, error) {
	info, err := os.Stat(item.Path)
	if err != nil {
		return Preview{}, fmt.Errorf("read staged scan: %w", err)
	}
	pageCount, err := pdftool.PageCount(context.Background(), item.Path)
	if err != nil {
		return Preview{}, fmt.Errorf("the scanner did not return a readable PDF: %w", err)
	}
	return Preview{
		UploadID:  item.ID,
		PageCount: pageCount,
		SizeBytes: info.Size(),
		ExpiresAt: item.ExpiresAt,
	}, nil
}

// Path returns the staged document's file, held until done is called, for
// serving the preview.
func Path(uploadID, ownerUserID string) (path string, done func(), ok bool) {
	item, ok := stagingRegistry.Hold(uploadID, ownerUserID)
	if !ok {
		return "", nil, false
	}
	return item.Path, func() { stagingRegistry.Unhold(item) }, true
}

// Discard throws away a scan the user did not keep.
func Discard(uploadID, ownerUserID string) bool {
	item, ok := stagingRegistry.Claim(uploadID, ownerUserID)
	if !ok {
		return false
	}
	stagingRegistry.Release(item)
	return true
}

// Save turns the staged document into a real one and returns its id. The
// staged file is consumed on success and left in place on failure, so a
// rejected save can be retried without scanning the pages again.
func Save(app core.App, uploadID, ownerUserID string) (string, error) {
	item, ok := stagingRegistry.Claim(uploadID, ownerUserID)
	if !ok {
		return "", ErrUploadNotFound
	}

	documentID, err := createDocument(app, ownerUserID, item)
	if err != nil {
		stagingRegistry.Restore(item)
		return "", err
	}
	stagingRegistry.Release(item)
	return documentID, nil
}

func createDocument(app core.App, ownerUserID string, item *stagedScan) (string, error) {
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(item.Path)
	if err != nil {
		return "", fmt.Errorf("read staged scan: %w", err)
	}
	if int64(len(data)) > maxScanBytes {
		return "", ErrTooLarge
	}

	fsFile, err := filesystem.NewFileFromBytes(data, documentName(time.Now()))
	if err != nil {
		return "", fmt.Errorf("prepare file: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("user", ownerUserID)
	record.Set("file", fsFile)
	record.Set("processing_status", models.DocStatusPending)

	// NormalizeSaveError is what makes a re-scan of a page already in the
	// library come back as *duplicates.ErrDuplicate instead of a unique-index
	// violation. The create hooks do limits, checksum and the processing job.
	if err := duplicates.NormalizeSaveError(app, record, app.Save(record)); err != nil {
		return "", err
	}
	return record.Id, nil
}

// documentName is what the document is called before the pipeline reads it and
// gives it a real title.
func documentName(now time.Time) string {
	return fmt.Sprintf("Scan %s.pdf", now.Format("2006-01-02 15.04.05"))
}
