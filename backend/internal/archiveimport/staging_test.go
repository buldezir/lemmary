package archiveimport

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/backup"
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
	manifest := &backup.Manifest{Format: backup.Format, Version: backup.Version}
	taxonomy := backup.Taxonomy{Tags: []string{"a", "b"}, DocumentTypes: []backup.NamedEntity{{Name: "Invoice"}}}

	preview := buildPreview("upload-1", "lemmary-export.zip", manifest, []Entry{
		{DocumentID: "a"},
		{DocumentID: "b", Duplicate: true, DuplicateOf: "doc_1"},
		{DocumentID: "c", Oversized: true},
		{DocumentID: "d", Missing: true},
		{DocumentID: "e"},
	}, taxonomy, 3)

	if preview.DocumentCount != 5 {
		t.Fatalf("document_count=%d want 5", preview.DocumentCount)
	}
	if preview.ImportableCount != 2 {
		t.Fatalf("importable=%d want 2", preview.ImportableCount)
	}
	if preview.DuplicateCount != 1 || preview.OversizedCount != 1 || preview.MissingCount != 1 {
		t.Fatalf("duplicate=%d oversized=%d missing=%d", preview.DuplicateCount, preview.OversizedCount, preview.MissingCount)
	}
	if preview.IgnoredCount != 3 || preview.TaxonomyCount != 3 {
		t.Fatalf("ignored=%d taxonomy=%d", preview.IgnoredCount, preview.TaxonomyCount)
	}
	if !preview.HasManifest || preview.FormatVersion != backup.Version {
		t.Fatalf("manifest=%v version=%d", preview.HasManifest, preview.FormatVersion)
	}
	if preview.UploadID != "upload-1" || preview.FileName != "lemmary-export.zip" {
		t.Fatalf("preview identity=%+v", preview)
	}
}

func TestBuildPreviewMarksLegacyArchive(t *testing.T) {
	preview := buildPreview("upload-2", "old.zip", nil, []Entry{{DocumentID: "a"}}, backup.Taxonomy{}, 0)
	if preview.HasManifest || preview.FormatVersion != 0 {
		t.Fatalf("preview=%+v", preview)
	}
}

func TestSaveArchiveLimits(t *testing.T) {
	dir := t.TempDir()

	if _, err := saveArchive(filepath.Join(dir, "empty.zip"), bytes.NewReader(nil), MaxArchiveBytes); !errors.Is(err, ErrNotArchive) {
		t.Fatalf("empty upload err=%v want ErrNotArchive", err)
	}

	path := filepath.Join(dir, "ok.zip")
	size, err := saveArchive(path, strings.NewReader("payload"), MaxArchiveBytes)
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
	if Discard("upload-1", "owner-b") {
		t.Fatal("another owner must not discard the archive")
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

	if _, err := Start(nil, "owner-a", "upload-1", ModeRestore); !errors.Is(err, ErrImportInProgress) {
		t.Fatalf("err=%v want ErrImportInProgress", err)
	}
	if _, ok := stagingRegistry.Claim("upload-1", "owner-a"); !ok {
		t.Fatal("a rejected start must leave the archive stageable")
	}
}

func TestStartRejectsUnknownUpload(t *testing.T) {
	resetStaging(t)
	if _, err := Start(nil, "owner-a", "nope", ModeRestore); !errors.Is(err, ErrUploadNotFound) {
		t.Fatalf("err=%v want ErrUploadNotFound", err)
	}
}

// The mode is checked before the archive is claimed, so a typo does not cost
// the user their staged upload.
func TestStartRejectsUnknownModeWithoutClaiming(t *testing.T) {
	resetStaging(t)
	dir := t.TempDir()
	stageFile(t, dir, "upload-1", "owner-a", time.Now().UTC().Add(time.Hour))

	if _, err := Start(nil, "owner-a", "upload-1", "bogus"); err == nil {
		t.Fatal("expected an error for an unknown mode")
	}
	if _, ok := stagingRegistry.Lookup("upload-1", "owner-a"); !ok {
		t.Fatal("the archive must still be staged")
	}
}
