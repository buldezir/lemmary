package limits

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/pdftool"
)

// SinglePage is what everything that is not a multi-page PDF counts as.
// Splitting a spreadsheet into notional pages would mean a number nobody can
// predict from looking at the file.
const SinglePage int64 = 1

// PageCountOfUpload decides PDF-ness from the first five bytes, never the name:
// a multi-page PDF renamed .txt would be charged one page while being stored as
// the PDF it is, so every page limit would fall to `mv`.
//
// Only an upload that passes the header is spooled to a temp file, which
// pdfinfo needs because it takes a path. A PDF whose page count cannot be read
// counts as SinglePage with a warning: refusing to store it would turn a bad
// limit interaction into data loss, and an unreadable PDF is the pipeline's to
// report against the stored document.
func PageCountOfUpload(logger *slog.Logger, file *filesystem.File) int64 {
	if file == nil {
		return SinglePage
	}
	if !hasPDFHeader(file) {
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

// hasPDFHeader mirrors pdfsplit's staging check on a reader rather than a path.
// Anything unreadable is treated as not-a-PDF, so the count falls back to one
// page and the real problem is the pipeline's to report.
func hasPDFHeader(file *filesystem.File) bool {
	if file.Reader == nil {
		return false
	}
	reader, err := file.Reader.Open()
	if err != nil {
		return false
	}
	defer reader.Close()

	var header [5]byte
	if _, err := io.ReadFull(reader, header[:]); err != nil {
		return false
	}
	return string(header[:]) == "%PDF-"
}

// spool writes an unsaved upload to a temp file, as worker does for a stored
// one. The temp name always carries .pdf, whatever the upload is called: only a
// file whose header said PDF reaches here, and pdftool keys off the extension.
// extension.
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

	tmp, err := os.CreateTemp("", "lemmary-limits-*.pdf")
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
