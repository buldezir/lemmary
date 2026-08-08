package textextract

import (
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
	_, err := docxText([]byte(`<w:document><w:t>oops`))
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
