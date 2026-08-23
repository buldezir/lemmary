package pdfsplit

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"paperless-go/backend/internal/pdftool/testpdf"
)

// resetStaging isolates the package-level registry between tests.
func resetStaging(t *testing.T) {
	t.Helper()
	clear := func() {
		stagingMu.Lock()
		stagingItems = map[string]*stagedPDF{}
		stagingBusy = map[string]struct{}{}
		stagingMu.Unlock()
	}
	clear()
	t.Cleanup(clear)
}

// stageDir registers a staged upload backed by a real directory.
func stageDir(t *testing.T, root, id, owner string, pageCount int, expiresAt time.Time) *stagedPDF {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, sourceFileName), testpdf.Multipage(pageCount), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	item := &stagedPDF{
		id:          id,
		ownerUserID: owner,
		dir:         dir,
		expiresAt:   expiresAt,
		preview:     Preview{UploadID: id, PageCount: pageCount, ExpiresAt: expiresAt},
	}
	stagingMu.Lock()
	stagingItems[id] = item
	stagingMu.Unlock()
	return item
}

func TestSavePDFRejectsUnsplittableUploads(t *testing.T) {
	dir := t.TempDir()

	if _, _, err := savePDF(filepath.Join(dir, "empty.pdf"), bytes.NewReader(nil)); !errors.Is(err, ErrNotPDF) {
		t.Fatalf("empty upload err=%v want ErrNotPDF", err)
	}
	if _, _, err := savePDF(filepath.Join(dir, "text.pdf"), strings.NewReader("not a pdf at all")); !errors.Is(err, ErrNotPDF) {
		t.Fatalf("non-PDF upload err=%v want ErrNotPDF", err)
	}
	if _, _, err := savePDF(filepath.Join(dir, "one.pdf"), bytes.NewReader(testpdf.Multipage(1))); !errors.Is(err, ErrSinglePage) {
		t.Fatalf("single page err=%v want ErrSinglePage", err)
	}
	if _, _, err := savePDF(filepath.Join(dir, "many.pdf"), bytes.NewReader(testpdf.Multipage(MaxPages+1))); !errors.Is(err, ErrTooManyPages) {
		t.Fatalf("too many pages err=%v want ErrTooManyPages", err)
	}
}

func TestSavePDFAcceptsAMultiPagePDF(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.pdf")
	source := testpdf.Multipage(4)

	pageCount, size, err := savePDF(path, bytes.NewReader(source))
	if err != nil {
		t.Fatalf("savePDF() error: %v", err)
	}
	if pageCount != 4 {
		t.Fatalf("page count=%d want 4", pageCount)
	}
	if size != int64(len(source)) {
		t.Fatalf("size=%d want %d", size, len(source))
	}
}

func TestSavePDFRejectsAnOversizedUpload(t *testing.T) {
	// A reader longer than the cap must be rejected without being streamed to
	// the end, so the assertion is on the error, not on the file.
	path := filepath.Join(t.TempDir(), "big.pdf")
	huge := strings.NewReader(strings.Repeat("x", int(MaxPDFBytes)+1))

	if _, _, err := savePDF(path, huge); !errors.Is(err, ErrPDFTooLarge) {
		t.Fatalf("oversized upload err=%v want ErrPDFTooLarge", err)
	}
}

func TestClaimStagedIsSingleUseAndOwnerScoped(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	item := stageDir(t, root, "upload-1", "owner-a", 3, time.Now().UTC().Add(time.Hour))

	if _, ok := claimStaged("upload-1", "owner-b"); ok {
		t.Fatal("another owner must not claim the upload")
	}
	if _, ok := claimStaged("missing", "owner-a"); ok {
		t.Fatal("unknown id must not claim")
	}

	claimed, ok := claimStaged("upload-1", "owner-a")
	if !ok || claimed != item {
		t.Fatalf("claim failed: %v %v", claimed, ok)
	}
	if _, ok := claimStaged("upload-1", "owner-a"); ok {
		t.Fatal("a claimed upload must not be claimable twice")
	}

	restoreStaged(claimed)
	if _, ok := claimStaged("upload-1", "owner-a"); !ok {
		t.Fatal("a restored upload must be claimable again")
	}
}

func TestClaimStagedRejectsExpired(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	stageDir(t, root, "old", "owner-a", 2, time.Now().UTC().Add(-time.Minute))

	if _, ok := claimStaged("old", "owner-a"); ok {
		t.Fatal("an expired upload must not be claimable")
	}
}

func TestLookupAndThumbPathAreOwnerScoped(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	item := stageDir(t, root, "upload-1", "owner-a", 3, time.Now().UTC().Add(time.Hour))

	view, sourcePath, ok := Lookup("upload-1", "owner-a")
	if !ok {
		t.Fatal("owner should be able to look up the upload")
	}
	if view.PageCount != 3 || sourcePath != item.sourcePath() {
		t.Fatalf("unexpected lookup: %+v %s", view, sourcePath)
	}
	if _, _, ok := Lookup("upload-1", "owner-b"); ok {
		t.Fatal("another owner must not look up the upload")
	}

	// Lookup must not consume the upload: the split still has to happen.
	if _, _, ok := Lookup("upload-1", "owner-a"); !ok {
		t.Fatal("lookup must not consume the upload")
	}

	if path, ok := ThumbPath("upload-1", "owner-a", 2); !ok || filepath.Dir(path) != item.dir {
		t.Fatalf("thumb path=%q ok=%v", path, ok)
	}
	for _, page := range []int{0, 4} {
		if _, ok := ThumbPath("upload-1", "owner-a", page); ok {
			t.Fatalf("page %d is out of range and must not resolve", page)
		}
	}
	if _, ok := ThumbPath("upload-1", "owner-b", 1); ok {
		t.Fatal("another owner must not read thumbnails")
	}
}

func TestDiscardRemovesTheUploadDirectory(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	item := stageDir(t, root, "upload-1", "owner-a", 2, time.Now().UTC().Add(time.Hour))

	if !Discard("upload-1", "owner-a") {
		t.Fatal("discard should report success")
	}
	if _, err := os.Stat(item.dir); !os.IsNotExist(err) {
		t.Fatalf("staged directory still present: %v", err)
	}
	if Discard("upload-1", "owner-a") {
		t.Fatal("discard must not succeed twice")
	}
}

func TestSweepStagingDropsExpiredAndOrphanDirectories(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	now := time.Now().UTC()

	stageDir(t, root, "fresh", "owner-a", 2, now.Add(time.Hour))
	expired := stageDir(t, root, "expired", "owner-a", 2, now.Add(-time.Minute))

	// Left behind by an earlier process: no registry entry, older than the TTL.
	orphan := filepath.Join(root, "orphan")
	if err := os.MkdirAll(orphan, 0o700); err != nil {
		t.Fatalf("mkdir orphan: %v", err)
	}
	stale := now.Add(-2 * stagingTTL)
	if err := os.Chtimes(orphan, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// A recent unknown directory may belong to an upload still being written.
	recent := filepath.Join(root, "recent")
	if err := os.MkdirAll(recent, 0o700); err != nil {
		t.Fatalf("mkdir recent: %v", err)
	}

	// An upload mid-split has no registry entry either, but must survive.
	splitting, ok := claimStaged("fresh", "owner-a")
	if !ok {
		t.Fatal("claim fresh")
	}

	sweepStaging(root, now)

	if _, err := os.Stat(splitting.dir); err != nil {
		t.Fatalf("upload being split was swept: %v", err)
	}
	if _, err := os.Stat(expired.dir); !os.IsNotExist(err) {
		t.Fatalf("expired upload kept: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan kept: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent unknown directory swept: %v", err)
	}
	stagingMu.Lock()
	_, stillRegistered := stagingItems["expired"]
	stagingMu.Unlock()
	if stillRegistered {
		t.Fatal("expired entry left in the registry")
	}
}
