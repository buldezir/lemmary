package worker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/duplicates"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/pdftool"
)

var ErrNotLocked = errors.New("worker: the document is not waiting for a password")

// PocketBase stores an upload as <name>_<10 random chars>.<ext>. Without
// stripping that, every unlock would stack a second suffix onto the name the
// owner downloads.
var storedNameSuffix = regexp.MustCompile(`_[a-z0-9]{10}(\.[^.]+)$`)

// Unlock replaces a password_required document's file with its decrypted copy
// and queues the full pipeline on it. A wrong password returns
// pdftool.ErrIncorrectPassword with the stored file untouched.
//
// The save goes through the documents update hooks, so page_count is counted
// again from the readable file and the limits apply to it.
func Unlock(ctx context.Context, app core.App, document *core.Record, password string) error {
	if document.GetString("processing_status") != models.DocStatusPasswordRequired {
		return ErrNotLocked
	}
	lockedName := document.GetString("file")
	lockedPath, _, cleanup, err := readDocumentToTempFile(app, document)
	if err != nil {
		// A concurrent unlock may have replaced the file already.
		if stillLocked(app, document.Id, lockedName) != nil {
			return ErrNotLocked
		}
		return err
	}
	defer cleanup()

	dir, err := os.MkdirTemp("", "lemmary-unlock-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	name := storedNameSuffix.ReplaceAllString(document.GetString("file"), "$1")
	openPath := filepath.Join(dir, name)
	if err := pdftool.Decrypt(ctx, lockedPath, openPath, password); err != nil {
		return err
	}

	file, err := filesystem.NewFileFromPath(openPath)
	if err != nil {
		return err
	}
	document.Set("file", file)
	if document.GetString("original_checksum") == "" {
		locked, err := os.Open(lockedPath)
		if err != nil {
			return err
		}
		checksum, err := duplicates.SHA256Reader(locked)
		locked.Close()
		if err != nil {
			return err
		}
		document.Set("original_checksum", checksum)
	}
	if err := duplicates.AssignChecksumFromUpload(app, document); err != nil {
		return err
	}
	return app.RunInTransaction(func(txApp core.App) error {
		// Checked again where the save is serialized: a second unlock of the
		// same document, or a job claimed meanwhile, has to lose here rather
		// than queue a second pipeline or swap the file under a running one.
		if err := stillLocked(txApp, document.Id, lockedName); err != nil {
			return err
		}
		document.Set("processing_status", models.DocStatusPending)
		if err := duplicates.NormalizeSaveError(txApp, document, txApp.Save(document)); err != nil {
			return err
		}
		steps := append([]string(nil), models.FullPipelineSteps...)
		_, err := createProcessingJob(txApp, document.Id, steps, nil, config.Overrides{})
		return err
	})
}

func stillLocked(app core.App, documentID, lockedName string) error {
	current, err := app.FindRecordById("documents", documentID)
	if err != nil {
		return err
	}
	if current.GetString("processing_status") != models.DocStatusPasswordRequired || current.GetString("file") != lockedName {
		return ErrNotLocked
	}
	return nil
}
