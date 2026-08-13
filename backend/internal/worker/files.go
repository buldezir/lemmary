package worker

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/pocketbase/pocketbase/core"
	"paperless-go/backend/internal/ocr"
)

func readDocumentToTempFile(app core.App, document *core.Record) (tmpPath, mimeType string, cleanup func(), err error) {
	fileName := document.GetString("file")
	if fileName == "" {
		return "", "", func() {}, fmt.Errorf("document has no file")
	}

	fsys, err := app.NewFilesystem()
	if err != nil {
		return "", "", func() {}, err
	}
	defer fsys.Close()

	fileKey := document.BaseFilesPath() + "/" + fileName
	reader, err := fsys.GetReader(fileKey)
	if err != nil {
		return "", "", func() {}, fmt.Errorf("open uploaded file: %w", err)
	}
	defer reader.Close()

	tmpFile, err := os.CreateTemp("", "paperless-doc-*"+filepath.Ext(fileName))
	if err != nil {
		return "", "", func() {}, err
	}
	tmpPath = tmpFile.Name()
	cleanup = func() { os.Remove(tmpPath) }

	if _, err := io.Copy(tmpFile, reader); err != nil {
		tmpFile.Close()
		cleanup()
		return "", "", func() {}, err
	}
	if err := tmpFile.Close(); err != nil {
		cleanup()
		return "", "", func() {}, err
	}

	return tmpPath, ocr.GuessMimeType(fileName), cleanup, nil
}

func ensureTempFile(state *StepState) error {
	if state.TmpPath != "" {
		return nil
	}

	tmpPath, mimeType, cleanup, err := readDocumentToTempFile(state.App, state.Document)
	if err != nil {
		return fmt.Errorf("read document file: %w", err)
	}
	state.TmpPath = tmpPath
	state.MimeType = mimeType
	if state.Cleanup == nil {
		state.Cleanup = cleanup
	}
	return nil
}
