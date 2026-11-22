package pdftool

import (
	"context"
	"time"

	"github.com/klippa-app/go-pdfium"
	"github.com/klippa-app/go-pdfium/requests"
)

// DecryptTimeout bounds one decryption, which rewrites the whole file.
const DecryptTimeout = 2 * time.Minute

// Decrypt writes pdfPath to outPath without its encryption. The objects are
// written back as they were, not re-rendered, so text layers and images come
// through unchanged, and the result is byte-stable across runs.
//
// The password stays in memory, passed only to this process's own PDFium
// worker; it is never on a command line and never part of a returned error.
func Decrypt(ctx context.Context, pdfPath, outPath, password string) error {
	if err := RequirePDF(pdfPath); err != nil {
		return err
	}
	err := withInstance(ctx, DecryptTimeout, func(instance pdfium.Pdfium) error {
		doc, err := openDocument(instance, pdfPath, password)
		if err != nil {
			return err
		}
		defer closeDocument(instance, doc)
		return saveAs(instance, doc, outPath, requests.SaveFlagRemoveSecurity)
	})
	if err != nil {
		return err
	}
	// The decrypted file becomes the stored original and its checksum, so it
	// has to come out the same every time, like an extracted part.
	return canonicalizeFileID(ctx, outPath, false)
}
