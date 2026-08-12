package appapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestParseExportMode(t *testing.T) {
	tests := []struct {
		in      string
		want    ExportMode
		wantErr bool
	}{
		{"", ExportModeOriginals, false},
		{"originals", ExportModeOriginals, false},
		{"ocr", ExportModeOCR, false},
		{"metadata", ExportModeMetadata, false},
		{"bogus", "", true},
	}
	for _, tt := range tests {
		got, err := ParseExportMode(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("ParseExportMode(%q) expected error", tt.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseExportMode(%q): %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ParseExportMode(%q)=%q want %q", tt.in, got, tt.want)
		}
	}
}

func TestWriteExportZipModes(t *testing.T) {
	ocrText := "Invoice OCR body"
	meta := map[string]any{
		"id":                "doc1",
		"title":             "Invoice",
		"tags":              []string{"finance"},
		"document_type":     "Invoice",
		"correspondent":     "Acme",
		"ocr_text":          ocrText,
		"original_filename": "invoice.pdf",
	}

	docs := []ExportDocument{
		{
			ID:               "doc1",
			OriginalFilename: "invoice.pdf",
			OpenFile: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("PDFBYTES")), nil
			},
			OCRText:  ocrText,
			Metadata: meta,
		},
		{
			ID:               "doc2",
			OriginalFilename: "note.txt",
			OpenFile: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("note body")), nil
			},
			OCRText:  "",
			Metadata: map[string]any{"id": "doc2", "title": "Note"},
		},
	}

	t.Run("originals", func(t *testing.T) {
		entries := mustZipEntries(t, ExportModeOriginals, docs)
		assertHas(t, entries, "doc1/invoice.pdf", "PDFBYTES")
		assertHas(t, entries, "doc2/note.txt", "note body")
		assertMissing(t, entries, "doc1/invoice.ocr.txt")
		assertMissing(t, entries, "doc1/invoice.metadata.json")
	})

	t.Run("ocr", func(t *testing.T) {
		entries := mustZipEntries(t, ExportModeOCR, docs)
		assertHas(t, entries, "doc1/invoice.pdf", "PDFBYTES")
		assertHas(t, entries, "doc1/invoice.ocr.txt", ocrText)
		assertMissing(t, entries, "doc2/note.ocr.txt") // empty OCR
		assertMissing(t, entries, "doc1/invoice.metadata.json")
	})

	t.Run("metadata", func(t *testing.T) {
		entries := mustZipEntries(t, ExportModeMetadata, docs)
		assertHas(t, entries, "doc1/invoice.pdf", "PDFBYTES")
		assertHas(t, entries, "doc1/invoice.ocr.txt", ocrText)
		raw, ok := entries["doc1/invoice.metadata.json"]
		if !ok {
			t.Fatal("missing metadata json")
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
			t.Fatalf("decode metadata: %v", err)
		}
		if decoded["title"] != "Invoice" {
			t.Fatalf("title=%v", decoded["title"])
		}
		if decoded["original_filename"] != "invoice.pdf" {
			t.Fatalf("original_filename=%v", decoded["original_filename"])
		}
		if decoded["ocr_text"] != ocrText {
			t.Fatalf("ocr_text=%v", decoded["ocr_text"])
		}
		assertHas(t, entries, "doc2/note.metadata.json", "")
	})
}

func TestWriteExportZipSkipsFailedOpen(t *testing.T) {
	docs := []ExportDocument{
		{
			ID:               "missing",
			OriginalFilename: "gone.pdf",
			OpenFile: func() (io.ReadCloser, error) {
				return nil, io.ErrUnexpectedEOF
			},
		},
		{
			ID:               "ok",
			OriginalFilename: "ok.txt",
			OpenFile: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("ok")), nil
			},
		},
	}
	entries := mustZipEntries(t, ExportModeOriginals, docs)
	if _, ok := entries["missing/gone.pdf"]; ok {
		t.Fatal("expected missing file to be skipped")
	}
	assertHas(t, entries, "ok/ok.txt", "ok")
}

func TestFileStem(t *testing.T) {
	if got := fileStem("invoice.pdf"); got != "invoice" {
		t.Fatalf("got %q", got)
	}
	if got := fileStem("archive"); got != "archive" {
		t.Fatalf("got %q", got)
	}
}

func mustZipEntries(t *testing.T, mode ExportMode, docs []ExportDocument) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := WriteExportZip(&buf, mode, docs); err != nil {
		t.Fatalf("WriteExportZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	out := make(map[string]string, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open %s: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read %s: %v", f.Name, err)
		}
		out[f.Name] = string(data)
	}
	return out
}

func assertHas(t *testing.T, entries map[string]string, name, contains string) {
	t.Helper()
	raw, ok := entries[name]
	if !ok {
		t.Fatalf("missing entry %q; have %#v", name, keys(entries))
	}
	if contains != "" && !strings.Contains(raw, contains) {
		t.Fatalf("entry %q missing %q; got %q", name, contains, raw)
	}
}

func assertMissing(t *testing.T, entries map[string]string, name string) {
	t.Helper()
	if _, ok := entries[name]; ok {
		t.Fatalf("unexpected entry %q", name)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
