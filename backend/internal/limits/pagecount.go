package limits

import (
	"context"
	"io"
	"log/slog"
	"os"

	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/pdftool"
)

// SinglePage is what everything that is not a multi-page PDF counts as. An
// image, a text file, a CSV, a DOCX or an XLSX is one document and one page for
// the purpose of an allowance; splitting a spreadsheet into notional pages would
// mean a number nobody can predict from looking at the file.
const SinglePage int64 = 1

// PageCountOfUpload reports how many pages an unsaved upload holds.
//
// Whether the upload is a PDF is decided by its first five bytes, never by its
// name. The name is the client's word, and every page limit would fall to `mv`
// if this trusted it: .txt, .csv and .docx are all accepted upload types, and a
// multi-page PDF renamed to any of them would be charged a single page while
// being stored as the PDF it is. pdfsplit's staging check makes the same point
// about a declared content type -- the header and a successful page count are
// the only things the file itself says.
//
// Reading the header costs one open of five bytes. Only an upload that passes it
// is spooled to a temp file, which pdfinfo needs because it takes a path and an
// upload is a reader, so an image or a text file still touches no disk here.
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

// hasPDFHeader reports whether the upload's first five bytes are a PDF header.
//
// Mirrors pdfsplit's staging check, on a reader rather than a path. Anything
// unreadable is treated as not-a-PDF: the page count then falls back to one
// page, and whatever is actually wrong with the file is the pipeline's to report
// against the stored document.
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

// spool writes an unsaved upload to a temp file, following the pattern
// worker.readDocumentToTempFile uses for a stored one: a path plus the cleanup
// that removes it.
//
// The temp name always carries .pdf, whatever the upload is called: only a file
// whose header said PDF reaches here, and the pdftool helpers key off the
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
