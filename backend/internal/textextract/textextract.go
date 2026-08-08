package textextract

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"
)

const (
	MIMEPlainText = "text/plain"
	MIMECSV       = "text/csv"
	MIMEDOCX      = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	MIMEXLSX      = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"

	// Cap expanded OOXML size well above the 20 MiB upload limit without
	// allowing multi-gigabyte zip bombs into memory.
	maxUncompressedBytes = 200 << 20
)

// Supports reports whether mimeType is extracted locally (no OCR provider).
func Supports(mimeType string) bool {
	switch mimeType {
	case MIMEPlainText, MIMECSV, MIMEDOCX, MIMEXLSX:
		return true
	default:
		return false
	}
}

// Extract reads born-digital document text from path based on mimeType.
func Extract(path, mimeType string) (string, error) {
	switch mimeType {
	case MIMEPlainText:
		return extractPlainText(path)
	case MIMECSV:
		return extractCSV(path)
	case MIMEDOCX:
		return extractDOCX(path)
	case MIMEXLSX:
		return extractXLSX(path)
	default:
		return "", fmt.Errorf("textextract does not support mime type %s", mimeType)
	}
}

func extractPlainText(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func extractCSV(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	reader := csv.NewReader(f)
	reader.FieldsPerRecord = -1
	reader.LazyQuotes = true
	var lines []string
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse csv: %w", err)
		}
		lines = append(lines, strings.Join(record, "\t"))
	}
	return strings.TrimSpace(strings.Join(lines, "\n")), nil
}

func extractDOCX(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer zr.Close()

	var parts []string
	var foundDocument bool
	for _, f := range zr.File {
		name := f.Name
		if name != "word/document.xml" &&
			!strings.HasPrefix(name, "word/header") &&
			!strings.HasPrefix(name, "word/footer") {
			continue
		}
		if !strings.HasSuffix(name, ".xml") {
			continue
		}
		if name == "word/document.xml" {
			foundDocument = true
		}
		text, err := readZipXMLText(f)
		if err != nil {
			return "", err
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	if !foundDocument {
		return "", fmt.Errorf("docx missing word/document.xml")
	}
	return strings.TrimSpace(strings.Join(parts, "\n")), nil
}

func readZipXMLText(f *zip.File) (string, error) {
	if f.UncompressedSize64 > maxUncompressedBytes {
		return "", fmt.Errorf("docx entry %q exceeds size limit", f.Name)
	}
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	data, err := io.ReadAll(io.LimitReader(rc, maxUncompressedBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxUncompressedBytes {
		return "", fmt.Errorf("docx entry %q exceeds size limit", f.Name)
	}
	return docxText(data)
}

func docxText(data []byte) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var parts []string
	var inText bool
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse docx xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				parts = append(parts, "\t")
			case "br", "cr":
				parts = append(parts, "\n")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				parts = append(parts, "\n")
			}
		case xml.CharData:
			if inText {
				parts = append(parts, string(t))
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "")), nil
}

func extractXLSX(path string) (string, error) {
	f, err := excelize.OpenFile(path, excelize.Options{UnzipSizeLimit: maxUncompressedBytes})
	if err != nil {
		return "", fmt.Errorf("open xlsx: %w", err)
	}
	defer f.Close()

	sheets := f.GetSheetList()
	var sections []string
	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			return "", fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		var lines []string
		if len(sheets) > 1 {
			lines = append(lines, "# "+sheet)
		}
		for _, row := range rows {
			lines = append(lines, strings.Join(row, "\t"))
		}
		if text := strings.TrimSpace(strings.Join(lines, "\n")); text != "" {
			sections = append(sections, text)
		}
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n")), nil
}
