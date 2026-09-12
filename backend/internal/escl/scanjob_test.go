package escl

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"lemmary/backend/internal/pdftool/testpdf"
)

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

// stageFile registers a staged scan backed by a real PDF.
func stageFile(t *testing.T, owner string, pageCount int) *stagedScan {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scan.pdf")
	if err := os.WriteFile(path, testpdf.Multipage(pageCount), 0o600); err != nil {
		t.Fatalf("write staged scan: %v", err)
	}
	item := &stagedScan{
		ID:          "scan1",
		OwnerUserID: owner,
		Path:        path,
		ExpiresAt:   time.Now().Add(stagingTTL),
		Payload:     &staged{preview: Preview{UploadID: "scan1", PageCount: pageCount}},
	}
	stagingRegistry.Add(item)
	return item
}

// The first scan writes the staged document; every later one appends to it.
func TestMergeStartsTheDocumentThenAppendsToIt(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdfunite")

	dir := t.TempDir()
	stagedPath := filepath.Join(dir, "scan.pdf")
	if err := os.WriteFile(stagedPath, nil, 0o600); err != nil {
		t.Fatalf("prepare staged scan: %v", err)
	}
	page := func(name string, pages int) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, testpdf.Multipage(pages), 0o600); err != nil {
			t.Fatalf("write page: %v", err)
		}
		return path
	}

	if err := merge(stagedPath, []string{page("first.pdf", 1)}); err != nil {
		t.Fatalf("merge() error: %v", err)
	}
	item := &stagedScan{ID: "x", Path: stagedPath, Payload: &staged{}}
	preview, err := describe(item)
	if err != nil {
		t.Fatalf("describe() error: %v", err)
	}
	if preview.PageCount != 1 {
		t.Fatalf("pages=%d want 1 after the first scan", preview.PageCount)
	}

	// A feeder hands back several pages from one job, so appending has to cope
	// with more than one at a time.
	if err := merge(stagedPath, []string{page("second.pdf", 1), page("third.pdf", 2)}); err != nil {
		t.Fatalf("merge() error: %v", err)
	}
	preview, err = describe(item)
	if err != nil {
		t.Fatalf("describe() error: %v", err)
	}
	if preview.PageCount != 4 {
		t.Fatalf("pages=%d want 4 after appending", preview.PageCount)
	}
	if preview.SizeBytes <= 0 {
		t.Fatal("the staged scan reported no size")
	}
}

func TestDiscardRemovesTheStagedFile(t *testing.T) {
	resetStaging(t)
	item := stageFile(t, "owner", 1)

	if !Discard(item.ID, "owner") {
		t.Fatal("Discard() should have found the scan")
	}
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
		t.Fatalf("the staged file survived the discard: %v", err)
	}
	if Discard(item.ID, "owner") {
		t.Fatal("a discarded scan should not be found twice")
	}
}

// Every staged scan is somebody's document, and the id is the only thing
// standing between two users.
func TestStagedScansAreNotVisibleToAnotherUser(t *testing.T) {
	resetStaging(t)
	item := stageFile(t, "owner", 1)

	if _, _, ok := Path(item.ID, "someone-else"); ok {
		t.Fatal("another user reached the staged scan")
	}
	if Discard(item.ID, "someone-else") {
		t.Fatal("another user discarded the staged scan")
	}

	path, done, ok := Path(item.ID, "owner")
	if !ok {
		t.Fatal("the owner could not reach their own scan")
	}
	done()
	if path != item.Path {
		t.Fatalf("path=%q want %q", path, item.Path)
	}
}

func TestDocumentNameCarriesTheScanTime(t *testing.T) {
	t.Parallel()

	got := documentName(time.Date(2026, 9, 9, 14, 30, 5, 0, time.UTC))
	if got != "Scan 2026-09-09 14.30.05.pdf" {
		t.Fatalf("documentName=%q", got)
	}
}
