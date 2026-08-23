package pdfsplit

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/pdftool"
	"lemmary/backend/internal/pdftool/testpdf"
)

// requirePoppler skips when a poppler binary the test needs is absent.
func requirePoppler(t *testing.T, binaries ...string) {
	t.Helper()
	for _, binary := range binaries {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s not installed", binary)
		}
	}
}

// resetStaging isolates the package-level registry between tests.
func resetStaging(t *testing.T) {
	t.Helper()
	stagingRegistry = newStagingRegistry()
	t.Cleanup(func() { stagingRegistry = newStagingRegistry() })
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
		ID:          id,
		OwnerUserID: owner,
		Path:        dir,
		ExpiresAt:   expiresAt,
		Payload: &staged{
			preview: Preview{UploadID: id, PageCount: pageCount, ExpiresAt: expiresAt},
		},
	}
	stagingRegistry.Add(item)
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

	if _, ok := stagingRegistry.Claim("upload-1", "owner-b"); ok {
		t.Fatal("another owner must not claim the upload")
	}
	if _, ok := stagingRegistry.Claim("missing", "owner-a"); ok {
		t.Fatal("unknown id must not claim")
	}

	claimed, ok := stagingRegistry.Claim("upload-1", "owner-a")
	if !ok || claimed != item {
		t.Fatalf("claim failed: %v %v", claimed, ok)
	}
	if _, ok := stagingRegistry.Claim("upload-1", "owner-a"); ok {
		t.Fatal("a claimed upload must not be claimable twice")
	}

	stagingRegistry.Restore(claimed)
	if _, ok := stagingRegistry.Claim("upload-1", "owner-a"); !ok {
		t.Fatal("a restored upload must be claimable again")
	}
}

func TestClaimStagedRejectsExpired(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	stageDir(t, root, "old", "owner-a", 2, time.Now().UTC().Add(-time.Minute))

	if _, ok := stagingRegistry.Claim("old", "owner-a"); ok {
		t.Fatal("an expired upload must not be claimable")
	}
}

// A held upload must survive everything that would otherwise delete it, and be
// cleaned up once the hold ends — that is what keeps a long detection run from
// reading a source.pdf somebody else deleted.
func TestHeldUploadSurvivesADiscardUntilTheHolderIsDone(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	item := stageDir(t, root, "upload-1", "owner-a", 2, time.Now().UTC().Add(time.Hour))

	held, ok := stagingRegistry.Hold("upload-1", "owner-a")
	if !ok {
		t.Fatal("hold failed")
	}
	// Holding must not consume the upload: the split still has to happen.
	if _, _, ok := Lookup("upload-1", "owner-a"); !ok {
		t.Fatal("a held upload must still be visible")
	}

	if !Discard("upload-1", "owner-a") {
		t.Fatal("discard should report success")
	}
	if _, err := os.Stat(sourcePathOf(item)); err != nil {
		t.Fatalf("the source was deleted while it was being read: %v", err)
	}

	stagingRegistry.Unhold(held)
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
		t.Fatalf("the discarded directory outlived the hold: %v", err)
	}
	if _, _, ok := Lookup("upload-1", "owner-a"); ok {
		t.Fatal("a discarded upload must not come back when the hold ends")
	}
}

func TestSweepKeepsAHeldUpload(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	now := time.Now().UTC()
	item := stageDir(t, root, "upload-1", "owner-a", 2, now.Add(time.Hour))

	held, ok := stagingRegistry.Hold("upload-1", "owner-a")
	if !ok {
		t.Fatal("hold failed")
	}
	// The TTL lapses while the detection is still reading the file.
	stagingRegistry.Sweep(root, now.Add(2*time.Hour))
	if _, err := os.Stat(sourcePathOf(item)); err != nil {
		t.Fatalf("the sweep deleted an upload being read: %v", err)
	}
	stagingRegistry.Unhold(held)
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
		t.Fatalf("the expired directory outlived the hold: %v", err)
	}
}

func TestLookupAndPageThumbAreOwnerScoped(t *testing.T) {
	resetStaging(t)
	requirePoppler(t, "pdftoppm")
	root := t.TempDir()
	item := stageDir(t, root, "upload-1", "owner-a", 3, time.Now().UTC().Add(time.Hour))

	view, sourcePath, ok := Lookup("upload-1", "owner-a")
	if !ok {
		t.Fatal("owner should be able to look up the upload")
	}
	if view.PageCount != 3 || sourcePath != sourcePathOf(item) {
		t.Fatalf("unexpected lookup: %+v %s", view, sourcePath)
	}
	if _, _, ok := Lookup("upload-1", "owner-b"); ok {
		t.Fatal("another owner must not look up the upload")
	}

	// Lookup must not consume the upload: the split still has to happen.
	if _, _, ok := Lookup("upload-1", "owner-a"); !ok {
		t.Fatal("lookup must not consume the upload")
	}

	// Nothing is rendered at staging time, so the first request has to produce
	// the PNG and the second has to reuse the cached file.
	cached := pdftool.PagePNGPath(item.Path, 2)
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Fatalf("page 2 was rendered before it was asked for: %v", err)
	}
	data, err := PageThumb("upload-1", "owner-a", 2)
	if err != nil {
		t.Fatalf("PageThumb() error: %v", err)
	}
	if !strings.HasPrefix(string(data), "\x89PNG") {
		t.Fatal("page 2 is not a PNG")
	}
	if _, err := os.Stat(cached); err != nil {
		t.Fatalf("the rendered page was not cached: %v", err)
	}
	if again, err := PageThumb("upload-1", "owner-a", 2); err != nil || len(again) != len(data) {
		t.Fatalf("cached read err=%v bytes=%d want %d", err, len(again), len(data))
	}

	for _, page := range []int{0, 4} {
		if _, err := PageThumb("upload-1", "owner-a", page); !errors.Is(err, ErrUploadNotFound) {
			t.Fatalf("page %d is out of range: err=%v", page, err)
		}
	}
	if _, err := PageThumb("upload-1", "owner-b", 1); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("another owner must not read thumbnails: err=%v", err)
	}
}

func TestDiscardRemovesTheUploadDirectory(t *testing.T) {
	resetStaging(t)
	root := t.TempDir()
	item := stageDir(t, root, "upload-1", "owner-a", 2, time.Now().UTC().Add(time.Hour))

	if !Discard("upload-1", "owner-a") {
		t.Fatal("discard should report success")
	}
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
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
	splitting, ok := stagingRegistry.Claim("fresh", "owner-a")
	if !ok {
		t.Fatal("claim fresh")
	}

	stagingRegistry.Sweep(root, now)

	if _, err := os.Stat(splitting.Path); err != nil {
		t.Fatalf("upload being split was swept: %v", err)
	}
	if _, err := os.Stat(expired.Path); !os.IsNotExist(err) {
		t.Fatalf("expired upload kept: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan kept: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent unknown directory swept: %v", err)
	}
	if _, ok := stagingRegistry.Lookup("expired", "owner-a"); ok {
		t.Fatal("expired entry left in the registry")
	}
}
