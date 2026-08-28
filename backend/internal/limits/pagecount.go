package limits

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/ocr"
	"lemmary/backend/internal/pdftool"
)

// SinglePage is what everything that is not a multi-page PDF counts as. An
// image, a text file, a CSV, a DOCX or an XLSX is one document and one page for
// the purpose of an allowance; splitting a spreadsheet into notional pages would
// mean a number nobody can predict from looking at the file.
const SinglePage int64 = 1

// PageCountOfUpload reports how many pages an unsaved upload holds.
//
// Only a PDF is inspected, and only by spooling it to a temp file first, because
// pdfinfo takes a path and the upload is a reader. That cost is paid by PDFs
// alone: every other accepted type returns SinglePage without touching the disk.
//
// A PDF whose page count cannot be read counts as SinglePage with a warning
// rather than failing the upload. pdfinfo failing means the file is not a PDF
// poppler can read, which is the processing pipeline's problem to report against
// the stored document -- refusing to store it here would turn a bad limit
// interaction into data loss.
func PageCountOfUpload(logger *slog.Logger, file *filesystem.File) int64 {
	if file == nil {
		return SinglePage
	}
	if ocr.GuessMimeType(file.Name) != "application/pdf" {
		return SinglePage
	}

	path, cleanup, err := spool(file)
	if err != nil {
		logWarn(logger, "page count fell back to one page: could not stage the upload", file.Name, err)
		return SinglePage
	}
	defer cleanup()

	count, err := pdftool.PageCount(context.Background(), path)
	if err != nil {
		logWarn(logger, "page count fell back to one page: unreadable PDF", file.Name, err)
		return SinglePage
	}
	if count < 1 {
		return SinglePage
	}
	return int64(count)
}

// spool writes an unsaved upload to a temp file, following the pattern
// worker.readDocumentToTempFile uses for a stored one: a path plus the cleanup
// that removes it.
func spool(file *filesystem.File) (path string, cleanup func(), err error) {
	noop := func() {}
	if file.Reader == nil {
		return "", noop, os.ErrInvalid
	}
	source, err := file.Reader.Open()
	if err != nil {
		return "", noop, err
	}
	defer source.Close()

	tmp, err := os.CreateTemp("", "lemmary-limits-*"+filepath.Ext(file.Name))
	if err != nil {
		return "", noop, err
	}
	path = tmp.Name()
	cleanup = func() { os.Remove(path) }

	if _, err := io.Copy(tmp, source); err != nil {
		tmp.Close()
		cleanup()
		return "", noop, err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return "", noop, err
	}
	return path, cleanup, nil
}

func logWarn(logger *slog.Logger, msg, name string, err error) {
	if logger == nil {
		return
	}
	logger.Warn(msg, "component", "limits", "file", name, slog.Any("error", err))
}
