package backup

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Document is one document ready to pack. OpenFile and OpenPreview are called
// during packing so the blobs stream straight from storage into the zip instead
// of being held in memory — a library of a few hundred PDFs would not fit
// otherwise.
type Document struct {
	ID               string
	Title            string
	OriginalFilename string
	// OpenFile returns the original upload. An error skips the whole document.
	OpenFile func() (io.ReadCloser, error)
	// OpenPreview returns the generated thumbnail, nil when there is none. An
	// error skips only the preview: it is regenerable, the document is not.
	OpenPreview func() (io.ReadCloser, error)
	OCRText     string
	Metadata    map[string]any
}

// Archive is everything one user's backup contains.
type Archive struct {
	Documents []Document
	Taxonomy  Taxonomy
	// ExportedAt is stamped into the manifest; zero uses time.Now().
	ExportedAt time.Time
}

// Write streams archive into w as a zip.
//
// Documents that fail OpenFile or lack an id or filename are skipped, because a
// blob missing from storage must not abort a backup of everything else. The
// manifest is written last, from the entries that were actually produced, so it
// never claims a document that is not in the archive.
func Write(w io.Writer, archive Archive) (err error) {
	zw := zip.NewWriter(w)
	defer func() {
		if closeErr := zw.Close(); err == nil {
			err = closeErr
		}
	}()

	manifest := Manifest{
		Format:     Format,
		Version:    Version,
		ExportedAt: exportedAt(archive.ExportedAt),
		Taxonomy:   normalizeTaxonomy(archive.Taxonomy),
		Documents:  make([]ManifestDocument, 0, len(archive.Documents)),
	}

	for _, doc := range archive.Documents {
		entry, ok, writeErr := writeDocument(zw, doc)
		if writeErr != nil {
			return writeErr
		}
		if !ok {
			continue
		}
		manifest.Documents = append(manifest.Documents, entry)
	}
	manifest.DocumentCount = len(manifest.Documents)

	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	if err := writeZipBytes(zw, ManifestName, payload); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// writeDocument packs one document's entries and returns its manifest row.
// ok is false when the document was skipped.
func writeDocument(zw *zip.Writer, doc Document) (ManifestDocument, bool, error) {
	if doc.OpenFile == nil || strings.TrimSpace(doc.OriginalFilename) == "" || strings.TrimSpace(doc.ID) == "" {
		return ManifestDocument{}, false, nil
	}

	reader, openErr := doc.OpenFile()
	if openErr != nil {
		return ManifestDocument{}, false, nil
	}

	ext := filepath.Ext(path.Base(doc.OriginalFilename))
	base := EntryBase(doc.ID, doc.Title)
	entry := ManifestDocument{ID: doc.ID, File: path.Join(Root, base+ext)}

	copyErr := writeZipFile(zw, entry.File, reader)
	_ = reader.Close()
	if copyErr != nil {
		return ManifestDocument{}, false, fmt.Errorf("write original %s: %w", entry.File, copyErr)
	}

	if text := strings.TrimSpace(doc.OCRText); text != "" {
		entry.OCR = path.Join(Root, base+OCRSuffix)
		if err := writeZipBytes(zw, entry.OCR, []byte(doc.OCRText)); err != nil {
			return ManifestDocument{}, false, fmt.Errorf("write ocr %s: %w", entry.OCR, err)
		}
	}

	meta := doc.Metadata
	if meta == nil {
		meta = map[string]any{}
	}
	payload, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return ManifestDocument{}, false, fmt.Errorf("marshal metadata for %s: %w", doc.ID, err)
	}
	entry.Metadata = path.Join(Root, base+MetadataSuffix)
	if err := writeZipBytes(zw, entry.Metadata, payload); err != nil {
		return ManifestDocument{}, false, fmt.Errorf("write metadata %s: %w", entry.Metadata, err)
	}

	if doc.OpenPreview != nil {
		if previewReader, err := doc.OpenPreview(); err == nil {
			previewPath := path.Join(Root, base+PreviewSuffix)
			copyErr := writeZipFile(zw, previewPath, previewReader)
			_ = previewReader.Close()
			if copyErr != nil {
				return ManifestDocument{}, false, fmt.Errorf("write preview %s: %w", previewPath, copyErr)
			}
			entry.Preview = previewPath
		}
	}

	return entry, true, nil
}

func exportedAt(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format(time.RFC3339)
}

// normalizeTaxonomy replaces nil slices with empty ones so the manifest always
// carries JSON arrays; an importer reading `null` would have to special-case it.
func normalizeTaxonomy(t Taxonomy) Taxonomy {
	if t.Tags == nil {
		t.Tags = []string{}
	}
	if t.Correspondents == nil {
		t.Correspondents = []NamedEntity{}
	}
	if t.DocumentTypes == nil {
		t.DocumentTypes = []NamedEntity{}
	}
	return t
}

func writeZipFile(zw *zip.Writer, name string, r io.Reader) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	return err
}

func writeZipBytes(zw *zip.Writer, name string, data []byte) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}
