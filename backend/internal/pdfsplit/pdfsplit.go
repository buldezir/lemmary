// Package pdfsplit splits a PDF that holds several separate documents joined
// into one file into one document per part.
//
// The upload is staged on disk and its pages are rendered to thumbnails first,
// so the user marks the cuts (or has them proposed by the extraction model)
// before any document is created. Confirming consumes the staged upload: the
// parts become documents and the original is discarded.
package pdfsplit

import (
	"errors"
	"fmt"
)

const (
	// MaxPDFBytes caps the staged upload. Parts are limited separately by the
	// documents.file field, so this only stops runaway uploads.
	MaxPDFBytes int64 = 100 << 20

	// MaxPages bounds one split: every page is rendered to a thumbnail at
	// upload time and every part queues a processing job.
	MaxPages = 100
)

// maxPartBytes matches the documents.file field limit, so a part that cannot be
// stored is reported as skipped instead of failing the whole run.
// A var so tests can shrink it instead of building 20 MB fixtures.
var maxPartBytes int64 = 20 << 20

var (
	// ErrNotPDF is returned when the upload is not a PDF poppler can read.
	ErrNotPDF = errors.New("the upload is not a readable PDF")
	// ErrPDFTooLarge is returned when the upload exceeds MaxPDFBytes.
	ErrPDFTooLarge = fmt.Errorf("the PDF is larger than %d bytes", MaxPDFBytes)
	// ErrTooManyPages is returned when the PDF exceeds MaxPages.
	ErrTooManyPages = fmt.Errorf("the PDF has more than %d pages", MaxPages)
	// ErrSinglePage is returned for a one-page PDF, which has nothing to split.
	ErrSinglePage = errors.New("the PDF has only one page")
	// ErrUploadNotFound is returned when the upload id is unknown, expired, or
	// belongs to someone else.
	ErrUploadNotFound = errors.New("upload not found or expired")
)
