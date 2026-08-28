package amazonimport

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/config"
)

// resetStaging isolates the package-level registry between tests.
func resetStaging(t *testing.T) {
	t.Helper()
	stagingRegistry = newStagingRegistry()
	t.Cleanup(func() { stagingRegistry = newStagingRegistry() })
}

func stageFile(t *testing.T, dir, id, owner string, expiresAt time.Time) *stagedArchive {
	t.Helper()
	path := filepath.Join(dir, id+".zip")
	if err := os.WriteFile(path, []byte("archive"), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	item := &stagedArchive{ID: id, OwnerUserID: owner, Path: path, ExpiresAt: expiresAt}
	stagingRegistry.Add(item)
	return item
}

func TestBuildPreviewCounts(t *testing.T) {
	preview := buildPreview("upload-1", "Your Orders.zip", []Entry{
		{Path: "a.pdf"},
		{Path: "b.pdf", Duplicate: true, DuplicateOf: "doc_1"},
		{Path: "c.pdf", Oversized: true},
		{Path: "d.pdf"},
	}, 7)

	if preview.PDFCount != 4 {
		t.Fatalf("pdf_count=%d want 4", preview.PDFCount)
	}
	if preview.ImportableCount != 2 {
		t.Fatalf("importable=%d want 2", preview.ImportableCount)
	}
	if preview.DuplicateCount != 1 || preview.OversizedCount != 1 {
		t.Fatalf("duplicate=%d oversized=%d want 1/1", preview.DuplicateCount, preview.OversizedCount)
	}
	if preview.IgnoredCount != 7 {
		t.Fatalf("ignored=%d want 7", preview.IgnoredCount)
	}
	if preview.UploadID != "upload-1" || preview.FileName != "Your Orders.zip" {
		t.Fatalf("preview identity=%+v", preview)
	}
}

func TestSaveArchiveLimits(t *testing.T) {
	dir := t.TempDir()

	if _, err := saveArchive(filepath.Join(dir, "empty.zip"), bytes.NewReader(nil), config.StagingMaxBytesFromEnv()); !errors.Is(err, ErrNotArchive) {
		t.Fatalf("empty upload err=%v want ErrNotArchive", err)
	}

	path := filepath.Join(dir, "ok.zip")
	size, err := saveArchive(path, strings.NewReader("payload"), config.StagingMaxBytesFromEnv())
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if size != 7 {
		t.Fatalf("size=%d want 7", size)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "payload" {
		t.Fatalf("staged file=%q err=%v", data, err)
	}

	// An upload over the cap is rejected rather than streamed to the end.
	big := strings.NewReader(strings.Repeat("x", 16))
	if _, err := saveArchive(filepath.Join(dir, "big.zip"), big, 8); !errors.Is(err, ErrArchiveTooLarge) {
		t.Fatalf("oversized upload err=%v want ErrArchiveTooLarge", err)
	}
}

func TestClaimStagedIsSingleUseAndOwnerScoped(t *testing.T) {
	resetStaging(t)
	dir := t.TempDir()
	item := stageFile(t, dir, "upload-1", "owner-a", time.Now().UTC().Add(time.Hour))

	if _, ok := stagingRegistry.Claim("upload-1", "owner-b"); ok {
		t.Fatal("another owner must not claim the archive")
	}
	if _, ok := stagingRegistry.Claim("missing", "owner-a"); ok {
		t.Fatal("unknown id must not claim")
	}

	claimed, ok := stagingRegistry.Claim("upload-1", "owner-a")
	if !ok || claimed != item {
		t.Fatalf("claim failed: %v %v", claimed, ok)
	}
	if _, ok := stagingRegistry.Claim("upload-1", "owner-a"); ok {
		t.Fatal("a claimed archive must not be claimable twice")
	}

	stagingRegistry.Restore(claimed)
	if _, ok := stagingRegistry.Claim("upload-1", "owner-a"); !ok {
		t.Fatal("a restored archive must be claimable again")
	}
}

func TestClaimStagedRejectsExpired(t *testing.T) {
	resetStaging(t)
	dir := t.TempDir()
	stageFile(t, dir, "old", "owner-a", time.Now().UTC().Add(-time.Minute))

	if _, ok := stagingRegistry.Claim("old", "owner-a"); ok {
		t.Fatal("an expired archive must not be claimable")
	}
}

func TestDiscardRemovesTheFile(t *testing.T) {
	resetStaging(t)
	dir := t.TempDir()
	item := stageFile(t, dir, "upload-1", "owner-a", time.Now().UTC().Add(time.Hour))

	if !Discard("upload-1", "owner-a") {
		t.Fatal("discard should report success")
	}
	if _, err := os.Stat(item.Path); !os.IsNotExist(err) {
		t.Fatalf("staged file still present: %v", err)
	}
	if Discard("upload-1", "owner-a") {
		t.Fatal("discard must not succeed twice")
	}
}

func TestSweepStagingDropsExpiredAndOrphanFiles(t *testing.T) {
	resetStaging(t)
	dir := t.TempDir()
	now := time.Now().UTC()

	stageFile(t, dir, "fresh", "owner-a", now.Add(time.Hour))
	expired := stageFile(t, dir, "expired", "owner-a", now.Add(-time.Minute))

	// Left behind by an earlier process: no registry entry, older than the TTL.
	orphan := filepath.Join(dir, "orphan.zip")
	if err := os.WriteFile(orphan, []byte("old"), 0o600); err != nil {
		t.Fatalf("write orphan: %v", err)
	}
	stale := now.Add(-2 * stagingTTL)
	if err := os.Chtimes(orphan, stale, stale); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	// A recent unknown file may belong to an upload still being written.
	recent := filepath.Join(dir, "recent.zip")
	if err := os.WriteFile(recent, []byte("new"), 0o600); err != nil {
		t.Fatalf("write recent: %v", err)
	}

	// An archive mid-import has no registry entry either, but must survive.
	importing, ok := stagingRegistry.Claim("fresh", "owner-a")
	if !ok {
		t.Fatal("claim fresh")
	}

	stagingRegistry.Sweep(dir, now)

	if _, err := os.Stat(importing.Path); err != nil {
		t.Fatalf("archive being imported was swept: %v", err)
	}
	if _, err := os.Stat(expired.Path); !os.IsNotExist(err) {
		t.Fatalf("expired archive kept: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan kept: %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent unknown file swept: %v", err)
	}
	if _, ok := stagingRegistry.Lookup("expired", "owner-a"); ok {
		t.Fatal("expired entry left in the registry")
	}
}

func TestStartRestoresArchiveWhenTheOwnerIsBusy(t *testing.T) {
	resetStaging(t)
	dir := t.TempDir()
	stageFile(t, dir, "upload-1", "owner-a", time.Now().UTC().Add(time.Hour))

	if err := registry.Acquire("owner-a"); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	t.Cleanup(func() { registry.Release("owner-a") })

	if _, err := Start(nil, "owner-a", "upload-1"); !errors.Is(err, ErrImportInProgress) {
		t.Fatalf("err=%v want ErrImportInProgress", err)
	}
	if _, ok := stagingRegistry.Claim("upload-1", "owner-a"); !ok {
		t.Fatal("a rejected start must leave the archive stageable")
	}
}

func TestStartRejectsUnknownUpload(t *testing.T) {
	resetStaging(t)
	if _, err := Start(nil, "owner-a", "nope"); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("err=%v want ErrUploadNotFound", err)
	}
}
