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
	"math"
	"os"
	"strconv"
	"strings"
)

const (
	// DefaultMaxUploadMB is the staged-upload cap when UPLOAD_MAX_MB is unset.
	DefaultMaxUploadMB = 100

	// MaxPages bounds one split: every page is rendered to a thumbnail at
	// upload time and every part queues a processing job.
	MaxPages = 100
)

// MaxPDFBytes caps the staged upload. Parts are limited separately by the
// documents.file field, so this only stops runaway uploads.
//
// Read from UPLOAD_MAX_MB at process start rather than from app_settings,
// because the cap protects the host as much as it shapes the product: staging a
// PDF costs several times its size in memory while its pages are rendered, so
// the operator who sized the machine decides, not the person uploading. Reading
// it from the environment also makes it a per-container value, which is the
// only kind an orchestrator can change — it recreates the container rather than
// reaching into a running one.
var MaxPDFBytes = maxPDFBytesFromEnv()

// maxPDFBytesFromEnv parses UPLOAD_MAX_MB, falling back on anything unusable.
//
// A malformed or non-positive value falls back rather than failing the boot: a
// typo in an orchestrator's environment must not take a customer's instance
// down, and the default is a working configuration.
func maxPDFBytesFromEnv() int64 {
	raw := strings.TrimSpace(os.Getenv("UPLOAD_MAX_MB"))
	if raw == "" {
		return DefaultMaxUploadMB << 20
	}
	mb, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || mb <= 0 || mb > math.MaxInt64>>20 {
		return DefaultMaxUploadMB << 20
	}
	return mb << 20
}

// maxPartBytes matches the documents.file field limit, so a part that cannot be
// stored is reported as skipped instead of failing the whole run.
// A var so tests can shrink it instead of building 20 MB fixtures.
var maxPartBytes int64 = 20 << 20

var (
	// ErrNotPDF is returned when the upload is not a PDF poppler can read.
	ErrNotPDF = errors.New("the upload is not a readable PDF")
	// ErrPDFTooLarge is returned when the upload exceeds MaxPDFBytes. The limit
	// is not in the message: MaxPDFBytes is read from the environment at start,
	// so a message built here would be formatted before it was known. savePDF
	// wraps this with the actual cap for the log.
	ErrPDFTooLarge = errors.New("the PDF is too large")
	// ErrTooManyPages is returned when the PDF exceeds MaxPages.
	ErrTooManyPages = fmt.Errorf("the PDF has more than %d pages", MaxPages)
	// ErrSinglePage is returned for a one-page PDF, which has nothing to split.
	ErrSinglePage = errors.New("the PDF has only one page")
	// ErrUploadNotFound is returned when the upload id is unknown, expired, or
	// belongs to someone else.
	ErrUploadNotFound = errors.New("upload not found or expired")
)
