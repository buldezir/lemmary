package appapi

import (
	"errors"
	"testing"

	"lemmary/backend/internal/zipimport"
)

// Anything this switch does not name falls through to a 500, which is what a
// zip bomb got while the sibling backup importer mapped it.
func TestArchiveErrorDetailNamesEveryRejection(t *testing.T) {
	rejections := []error{
		zipimport.ErrNotArchive,
		zipimport.ErrNoFiles,
		zipimport.ErrTooManyFiles,
		zipimport.ErrArchiveTooLarge,
		zipimport.ErrArchiveTooDense,
	}
	for _, src := range []zipimport.Source{zipimport.SourceAmazon, zipimport.SourceFiles} {
		for _, err := range rejections {
			if detail := archiveErrorDetail(src, err); detail == "" {
				t.Errorf("source %q: %v has no client-facing message, so it would be a 500", src, err)
			}
		}
	}
}

// The noun is the only thing the source changes: "no PDF files" helps someone
// who uploaded the wrong Amazon export and misleads someone with photos.
func TestArchiveErrorDetailWordsTheEmptyArchivePerSource(t *testing.T) {
	amazon := archiveErrorDetail(zipimport.SourceAmazon, zipimport.ErrNoFiles)
	files := archiveErrorDetail(zipimport.SourceFiles, zipimport.ErrNoFiles)
	if amazon != "No PDF files found in the archive." {
		t.Errorf("amazon=%q", amazon)
	}
	if files != "No importable files found in the archive." {
		t.Errorf("files=%q", files)
	}
}

// A failure that is not the caller's fault must not be dressed up as one.
func TestArchiveErrorDetailStaysQuietOnServerFaults(t *testing.T) {
	if detail := archiveErrorDetail(zipimport.SourceFiles, errors.New("disk on fire")); detail != "" {
		t.Errorf("detail=%q want empty", detail)
	}
}
