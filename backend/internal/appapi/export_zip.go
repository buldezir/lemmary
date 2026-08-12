package appapi

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"unicode"
)

const exportZipRoot = "paperless-export"

// ExportMode controls which sidecar files are included in the archive.
type ExportMode string

const (
	ExportModeOriginals ExportMode = "originals"
	ExportModeOCR       ExportMode = "ocr"
	ExportModeMetadata  ExportMode = "metadata"
)

// ParseExportMode validates the query value; empty defaults to originals.
func ParseExportMode(raw string) (ExportMode, error) {
	switch strings.TrimSpace(raw) {
	case "", string(ExportModeOriginals):
		return ExportModeOriginals, nil
	case string(ExportModeOCR):
		return ExportModeOCR, nil
	case string(ExportModeMetadata):
		return ExportModeMetadata, nil
	default:
		return "", fmt.Errorf("invalid mode %q; use originals, ocr, or metadata", raw)
	}
}

// ExportDocument is one document entry ready to pack into a zip.
// OpenFile is called when packing; it may return an error to skip the document.
type ExportDocument struct {
	ID               string
	Title            string
	OriginalFilename string
	OpenFile         func() (io.ReadCloser, error)
	OCRText          string
	Metadata         map[string]any
}

// WriteExportZip streams a zip of the given documents for mode into w.
// Entries are flattened under paperless-export/ as "[id] title.ext".
// Documents that fail OpenFile or lack an ID/filename are skipped.
// Empty OCR text omits the OCR sidecar.
func WriteExportZip(w io.Writer, mode ExportMode, docs []ExportDocument) (err error) {
	zw := zip.NewWriter(w)
	defer func() {
		if closeErr := zw.Close(); err == nil {
			err = closeErr
		}
	}()

	for _, doc := range docs {
		if doc.OpenFile == nil || strings.TrimSpace(doc.OriginalFilename) == "" || strings.TrimSpace(doc.ID) == "" {
			continue
		}

		reader, openErr := doc.OpenFile()
		if openErr != nil {
			continue
		}

		ext := filepath.Ext(path.Base(doc.OriginalFilename))
		base := exportEntryBase(doc.ID, doc.Title)
		originalPath := path.Join(exportZipRoot, base+ext)

		copyErr := writeZipFile(zw, originalPath, reader)
		_ = reader.Close()
		if copyErr != nil {
			return fmt.Errorf("write original %s: %w", originalPath, copyErr)
		}

		includeOCR := mode == ExportModeOCR || mode == ExportModeMetadata
		if includeOCR {
			if text := strings.TrimSpace(doc.OCRText); text != "" {
				ocrPath := path.Join(exportZipRoot, base+".ocr.txt")
				if err = writeZipBytes(zw, ocrPath, []byte(doc.OCRText)); err != nil {
					return fmt.Errorf("write ocr %s: %w", ocrPath, err)
				}
			}
		}

		if mode == ExportModeMetadata {
			meta := doc.Metadata
			if meta == nil {
				meta = map[string]any{}
			}
			var payload []byte
			payload, err = json.MarshalIndent(meta, "", "  ")
			if err != nil {
				return fmt.Errorf("marshal metadata for %s: %w", doc.ID, err)
			}
			metaPath := path.Join(exportZipRoot, base+".metadata.json")
			if err = writeZipBytes(zw, metaPath, payload); err != nil {
				return fmt.Errorf("write metadata %s: %w", metaPath, err)
			}
		}
	}

	return nil
}

// exportEntryBase returns "[id] title" with a filesystem-safe title.
func exportEntryBase(id, title string) string {
	safeTitle := sanitizeExportTitle(title)
	return "[" + id + "] " + safeTitle
}

func sanitizeExportTitle(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return "Untitled"
	}
	var b strings.Builder
	b.Grow(len(title))
	prevSpace := false
	for _, r := range title {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			continue
		case r == 0 || unicode.IsControl(r):
			continue
		case unicode.IsSpace(r):
			if prevSpace || b.Len() == 0 {
				continue
			}
			b.WriteByte(' ')
			prevSpace = true
		default:
			b.WriteRune(r)
			prevSpace = false
		}
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "Untitled"
	}
	// Avoid names that are only "." / ".." after sanitizing.
	if out == "." || out == ".." {
		return "Untitled"
	}
	return out
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
