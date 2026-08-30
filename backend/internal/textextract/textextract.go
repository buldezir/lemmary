package textextract

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/xuri/excelize/v2"

	"lemmary/backend/internal/models"
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

// maxTextRunes bounds what one document may extract to.
//
// A plain text or CSV file needs no bound: a rune is at least one byte, so it
// cannot yield more characters than the 20 MiB documents.file cap let through.
// An OOXML file can, and not only by being a zip bomb -- maxUncompressedBytes
// bounds the XML read in, and XLSX resolves shared strings on the way out, so a
// sheet of a million cells pointing at one string produces far more text than
// the bytes it was stored in. Counting as the text accumulates is the only
// thing that catches that, and it is cheap: no provider call is involved and
// abandoning a local parse costs nothing.
//
// Without it the overflow surfaced as a field-validation error from inside
// app.Save when the OCR step tried to store the result, which failed the
// document rather than the extraction.
//
// A var so tests can shrink it instead of building 20 MB fixtures, the way
// pdfsplit.maxPartBytes does.
var maxTextRunes = models.MaxOCRTextRunes

// ErrTooMuchText is returned when a file's text runs past maxTextRunes.
var ErrTooMuchText = errors.New("document holds more text than can be stored")

// budget counts runes down from maxTextRunes.
//
// Runes, not bytes, because that is the unit the column is declared in:
// PocketBase measures a text field's Max as len([]rune(value)).
type budget struct{ left int }

func newBudget() *budget { return &budget{left: maxTextRunes} }

// take charges s against the budget, reporting whether it still fits.
func (b *budget) take(s string) bool {
	b.left -= utf8.RuneCountInString(s)
	return b.left >= 0
}

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
	// One budget for the whole document: headers and footers are text too, and
	// a file that stayed under the ceiling per entry could still cross it once
	// they are joined.
	remaining := newBudget()
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
		text, err := readZipXMLText(f, remaining)
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

func readZipXMLText(f *zip.File, remaining *budget) (string, error) {
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
	return docxText(data, remaining)
}

func docxText(data []byte, remaining *budget) (string, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var parts []string
	var inText bool
	// Charged as each piece is appended rather than at the end, so a
	// document.xml full of text stops being decoded as soon as the answer is
	// known.
	add := func(s string) bool {
		parts = append(parts, s)
		return remaining.take(s)
	}
	for {
		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse docx xml: %w", err)
		}
		var ok = true
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				inText = true
			case "tab":
				ok = add("\t")
			case "br", "cr":
				ok = add("\n")
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "p":
				ok = add("\n")
			}
		case xml.CharData:
			if inText {
				ok = add(string(t))
			}
		}
		if !ok {
			return "", ErrTooMuchText
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
	// One budget across every sheet, and the row iterator rather than GetRows:
	// a sheet's text can run far past the bytes it was stored in, because the
	// cells hold indexes into the shared string table and this is where they
	// are resolved. GetRows would build that whole expansion in memory before
	// anything could measure it.
	remaining := newBudget()
	var sections []string
	for _, sheet := range sheets {
		rows, err := f.Rows(sheet)
		if err != nil {
			return "", fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		var lines []string
		if len(sheets) > 1 {
			lines = append(lines, "# "+sheet)
		}
		for rows.Next() {
			row, err := rows.Columns()
			if err != nil {
				rows.Close()
				return "", fmt.Errorf("read sheet %q: %w", sheet, err)
			}
			line := strings.Join(row, "\t")
			if !remaining.take(line) {
				rows.Close()
				return "", ErrTooMuchText
			}
			lines = append(lines, line)
		}
		if err := rows.Close(); err != nil {
			return "", fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		if err := rows.Error(); err != nil {
			return "", fmt.Errorf("read sheet %q: %w", sheet, err)
		}
		if text := strings.TrimSpace(strings.Join(lines, "\n")); text != "" {
			sections = append(sections, text)
		}
	}
	return strings.TrimSpace(strings.Join(sections, "\n\n")), nil
}
