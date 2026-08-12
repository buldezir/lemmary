package appapi

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
)

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
	OriginalFilename string
	OpenFile         func() (io.ReadCloser, error)
	OCRText          string
	Metadata         map[string]any
}

// WriteExportZip streams a zip of the given documents for mode into w.
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

		baseName := path.Base(doc.OriginalFilename)
		dir := doc.ID
		originalPath := path.Join(dir, baseName)

		copyErr := writeZipFile(zw, originalPath, reader)
		_ = reader.Close()
		if copyErr != nil {
			return fmt.Errorf("write original %s: %w", originalPath, copyErr)
		}

		stem := fileStem(baseName)
		includeOCR := mode == ExportModeOCR || mode == ExportModeMetadata
		if includeOCR {
			if text := strings.TrimSpace(doc.OCRText); text != "" {
				ocrPath := path.Join(dir, stem+".ocr.txt")
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
			metaPath := path.Join(dir, stem+".metadata.json")
			if err = writeZipBytes(zw, metaPath, payload); err != nil {
				return fmt.Errorf("write metadata %s: %w", metaPath, err)
			}
		}
	}

	return nil
}

func fileStem(filename string) string {
	base := path.Base(filename)
	ext := filepath.Ext(base)
	if ext == "" {
		return base
	}
	return strings.TrimSuffix(base, ext)
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
