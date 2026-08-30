package textextract

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSupports(t *testing.T) {
	cases := []struct {
		mime string
		want bool
	}{
		{MIMEPlainText, true},
		{MIMECSV, true},
		{MIMEDOCX, true},
		{MIMEXLSX, true},
		{"application/pdf", false},
		{"image/png", false},
	}
	for _, tc := range cases {
		if got := Supports(tc.mime); got != tc.want {
			t.Errorf("Supports(%q)=%v want %v", tc.mime, got, tc.want)
		}
	}
}

func TestExtractPlainText(t *testing.T) {
	text, err := Extract(filepath.Join("testdata", "sample.txt"), MIMEPlainText)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "INV-9001") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestExtractCSV(t *testing.T) {
	text, err := Extract(filepath.Join("testdata", "sample.csv"), MIMECSV)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Paper") || !strings.Contains(text, "25.00") {
		t.Fatalf("unexpected text: %q", text)
	}
	if !strings.Contains(text, "\t") {
		t.Fatalf("expected tab-separated fields, got %q", text)
	}
}

func TestExtractCSVLazyQuotes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lazy.csv")
	// Unescaped quote inside a field — rejected without LazyQuotes.
	if err := os.WriteFile(path, []byte("a,b\nhe said \"hi\",2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := Extract(path, MIMECSV)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "hi") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestDocxTextRejectsCorruptXML(t *testing.T) {
	_, err := docxText([]byte(`<w:document><w:t>oops`), newBudget())
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestExtractDOCX(t *testing.T) {
	text, err := Extract(filepath.Join("testdata", "sample.docx"), MIMEDOCX)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "INV-9002") || !strings.Contains(text, "Acme Word Docs") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestExtractXLSX(t *testing.T) {
	text, err := Extract(filepath.Join("testdata", "sample.xlsx"), MIMEXLSX)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Toner") || !strings.Contains(text, "INV-9003") {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestExtractUnsupported(t *testing.T) {
	_, err := Extract(filepath.Join("testdata", "sample.txt"), "application/pdf")
	if err == nil {
		t.Fatal("expected error for unsupported mime")
	}
}

// shrinkBudget lowers the rune budget for one test, so a case that crosses it
// is a handful of bytes rather than a 20 MB fixture.
func shrinkBudget(t *testing.T, runes int) {
	t.Helper()
	previous := maxTextRunes
	maxTextRunes = runes
	t.Cleanup(func() { maxTextRunes = previous })
}

func TestDocxTextStopsAtTheBudget(t *testing.T) {
	shrinkBudget(t, 8)
	// Nine characters of body text, one over.
	_, err := docxText([]byte(`<w:document><w:t>123456789</w:t></w:document>`), newBudget())
	if !errors.Is(err, ErrTooMuchText) {
		t.Fatalf("got %v, want ErrTooMuchText", err)
	}
}

func TestDocxTextAcceptsExactlyTheBudget(t *testing.T) {
	// Eight characters of text plus the newline </w:p> would add -- there is no
	// paragraph here, so the budget is spent on the text alone.
	shrinkBudget(t, 8)
	text, err := docxText([]byte(`<w:document><w:t>12345678</w:t></w:document>`), newBudget())
	if err != nil {
		t.Fatal(err)
	}
	if text != "12345678" {
		t.Fatalf("got %q", text)
	}
}

func TestDocxBudgetIsSharedAcrossEntries(t *testing.T) {
	// Header and body each fit alone; together they do not. Extraction joins
	// them into one field, so the budget has to span both.
	shrinkBudget(t, 12)
	path := writeZip(t, "budget.docx", map[string]string{
		"word/document.xml": `<w:document><w:t>bodybody</w:t></w:document>`,
		"word/header1.xml":  `<w:hdr><w:t>headerhead</w:t></w:hdr>`,
	})
	if _, err := Extract(path, MIMEDOCX); !errors.Is(err, ErrTooMuchText) {
		t.Fatalf("got %v, want ErrTooMuchText", err)
	}
}

// TestXlsxBudgetCountsResolvedSharedStrings is the case the budget exists for.
//
// A shared string is stored once and referenced by every cell that uses it, so
// the text an XLSX extracts to is not bounded by the bytes it arrived in. Here
// one 40-character string is referenced ten times: the sheet XML is a few
// hundred bytes and the extracted text is 400 characters. No check on the
// upload's size, compressed or not, can see that coming.
func TestXlsxBudgetCountsResolvedSharedStrings(t *testing.T) {
	shrinkBudget(t, 200)

	const shared = "0123456789012345678901234567890123456789"
	var cells strings.Builder
	for row := 1; row <= 10; row++ {
		fmt.Fprintf(&cells, `<row r="%d"><c r="A%d" t="s"><v>0</v></c></row>`, row, row)
	}
	path := writeZip(t, "shared.xlsx", map[string]string{
		"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
			`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>` +
			`<Override PartName="/xl/sharedStrings.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sharedStrings+xml"/>` +
			`</Types>`,
		"_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>` +
			`</Relationships>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>` +
			`</Relationships>`,
		"xl/workbook.xml": `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" ` +
			`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
			`<sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/sharedStrings.xml": `<sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" count="10" uniqueCount="1">` +
			`<si><t>` + shared + `</t></si></sst>`,
		"xl/worksheets/sheet1.xml": `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">` +
			`<sheetData>` + cells.String() + `</sheetData></worksheet>`,
	})

	if _, err := Extract(path, MIMEXLSX); !errors.Is(err, ErrTooMuchText) {
		t.Fatalf("got %v, want ErrTooMuchText", err)
	}
}

func writeZip(t *testing.T, name string, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for entryName, body := range entries {
		w, err := zw.Create(entryName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
