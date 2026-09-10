package zipimport

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/staging"
)

// stagingTTL is how long an uploaded archive waits for confirmation before it
// is swept. The user only has to answer one question, so this is generous.
const stagingTTL = 30 * time.Minute

// Preview describes a staged archive and is what the confirmation step renders.
type Preview struct {
	UploadID  string    `json:"upload_id"`
	FileName  string    `json:"file_name"`
	ExpiresAt time.Time `json:"expires_at"`
	// FileCount is every importable file in the archive, duplicates included.
	FileCount int `json:"file_count"`
	// ImportableCount is how many of those would become new documents.
	ImportableCount int `json:"importable_count"`
	DuplicateCount  int `json:"duplicate_count"`
	OversizedCount  int `json:"oversized_count"`
	// IgnoredCount is the entries this source does not import: a type the
	// documents collection cannot store, or -- for an Amazon export -- its CSV
	// reports and delivery photos.
	IgnoredCount int     `json:"ignored_count"`
	Files        []Entry `json:"files"`

	// Source is carried for the import run, not for the client: runImport must
	// filter the archive with the same source the preview was built with, or the
	// by-position match it does lines entries up against the wrong bytes.
	Source Source `json:"-"`
}

// stagedArchive is one upload waiting to be imported.
type stagedArchive = staging.Item[Preview]

var stagingRegistry = newStagingRegistry()

func newStagingRegistry() *staging.Registry[Preview] {
	return staging.New[Preview](staging.Config{
		TTL: stagingTTL,
		// An upload is a single .zip file in the shared staging directory.
		Remove:  os.Remove,
		Manages: staging.Files,
	})
}

// Inspect stages the uploaded archive on disk and describes the files it holds
// for this source. Nothing is imported until Start is called with the returned
// upload id.
func Inspect(app core.App, src Source, ownerUserID, fileName string, r io.Reader) (Preview, error) {
	if strings.TrimSpace(ownerUserID) == "" {
		return Preview{}, fmt.Errorf("owner user id is required")
	}

	dir := stagingDir(app)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Preview{}, fmt.Errorf("prepare staging dir: %w", err)
	}
	stagingRegistry.Sweep(dir, time.Now())
	// One staged upload per owner. The confirmation step only ever works on the
	// newest one, so an earlier upload is already dead weight -- and without
	// this an account could stage archive after archive and fill the volume
	// without ever confirming an import.
	stagingRegistry.DiscardOwned(ownerUserID)

	id, err := staging.NewID()
	if err != nil {
		return Preview{}, err
	}
	archivePath := filepath.Join(dir, id+".zip")

	size, err := saveArchive(archivePath, r, config.StagingMaxBytesFromEnv())
	if err != nil {
		os.Remove(archivePath)
		return Preview{}, err
	}

	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		os.Remove(archivePath)
		return Preview{}, ErrNotArchive
	}
	entries, ignored, err := scan(src, documentLookup(app, ownerUserID), &zr.Reader)
	zr.Close()
	if err != nil {
		os.Remove(archivePath)
		return Preview{}, err
	}

	item := &stagedArchive{
		ID:          id,
		OwnerUserID: ownerUserID,
		Path:        archivePath,
		ExpiresAt:   time.Now().UTC().Add(stagingTTL),
		Payload:     buildPreview(src, id, fileName, entries, ignored),
	}
	item.Payload.ExpiresAt = item.ExpiresAt
	stagingRegistry.Add(item)

	app.Logger().Info("archive staged",
		"component", "zip_import",
		"source", src,
		"upload_id", id,
		"bytes", size,
		"files", item.Payload.FileCount,
		"importable", item.Payload.ImportableCount,
	)
	return item.Payload, nil
}

func buildPreview(src Source, uploadID, fileName string, entries []Entry, ignored int) Preview {
	preview := Preview{
		UploadID:     uploadID,
		FileName:     strings.TrimSpace(fileName),
		FileCount:    len(entries),
		IgnoredCount: ignored,
		Files:        entries,
		Source:       src,
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
	item, ok := stagingRegistry.Claim(uploadID, ownerUserID)
	if !ok {
		return false
	}
	stagingRegistry.Release(item)
	return true
}

func stagingDir(app core.App) string {
	return filepath.Join(app.DataDir(), "temp", "zip_import")
}
