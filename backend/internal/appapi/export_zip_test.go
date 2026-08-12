package appapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"unicode/utf8"
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
		"original_filename": "invoice.pdf",
	}

	docs := []ExportDocument{
		{
			ID:               "doc1",
			Title:            "Invoice",
			OriginalFilename: "invoice.pdf",
			OpenFile: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("PDFBYTES")), nil
			},
			OCRText:  ocrText,
			Metadata: meta,
		},
		{
			ID:               "doc2",
			Title:            "Note",
			OriginalFilename: "note.txt",
			OpenFile: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("note body")), nil
			},
			OCRText:  "",
			Metadata: map[string]any{"id": "doc2", "title": "Note"},
		},
	}

	orig1 := "paperless-export/[doc1] Invoice.pdf"
	ocr1 := "paperless-export/[doc1] Invoice.ocr.txt"
	meta1 := "paperless-export/[doc1] Invoice.metadata.json"
	orig2 := "paperless-export/[doc2] Note.txt"
	meta2 := "paperless-export/[doc2] Note.metadata.json"

	t.Run("originals", func(t *testing.T) {
		entries := mustZipEntries(t, ExportModeOriginals, docs)
		assertHas(t, entries, orig1, "PDFBYTES")
		assertHas(t, entries, orig2, "note body")
		assertMissing(t, entries, ocr1)
		assertMissing(t, entries, meta1)
	})

	t.Run("ocr", func(t *testing.T) {
		entries := mustZipEntries(t, ExportModeOCR, docs)
		assertHas(t, entries, orig1, "PDFBYTES")
		assertHas(t, entries, ocr1, ocrText)
		assertMissing(t, entries, "paperless-export/[doc2] Note.ocr.txt")
		assertMissing(t, entries, meta1)
	})

	t.Run("metadata", func(t *testing.T) {
		entries := mustZipEntries(t, ExportModeMetadata, docs)
		assertHas(t, entries, orig1, "PDFBYTES")
		assertHas(t, entries, ocr1, ocrText)
		raw, ok := entries[meta1]
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
		if _, hasOCR := decoded["ocr_text"]; hasOCR {
			t.Fatal("metadata json must not include ocr_text")
		}
		assertHas(t, entries, meta2, "")
	})
}

func TestWriteExportZipSkipsFailedOpen(t *testing.T) {
	docs := []ExportDocument{
		{
			ID:               "missing",
			Title:            "Gone",
			OriginalFilename: "gone.pdf",
			OpenFile: func() (io.ReadCloser, error) {
				return nil, io.ErrUnexpectedEOF
			},
		},
		{
			ID:               "ok",
			Title:            "Ok",
			OriginalFilename: "ok.txt",
			OpenFile: func() (io.ReadCloser, error) {
				return io.NopCloser(strings.NewReader("ok")), nil
			},
		},
	}
	entries := mustZipEntries(t, ExportModeOriginals, docs)
	if _, ok := entries["paperless-export/[missing] Gone.pdf"]; ok {
		t.Fatal("expected missing file to be skipped")
	}
	assertHas(t, entries, "paperless-export/[ok] Ok.txt", "ok")
}

func TestExportEntryBase(t *testing.T) {
	if got := exportEntryBase("abc", "Invoice / Q1"); got != "[abc] Invoice Q1" {
		t.Fatalf("got %q", got)
	}
	if got := exportEntryBase("abc", "  "); got != "[abc] Untitled" {
		t.Fatalf("got %q", got)
	}
	if got := sanitizeExportTitle(`bad:name*?<>|`); got != "badname" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeExportTitleTruncates(t *testing.T) {
	long := strings.Repeat("a", maxExportTitleBytes+50)
	got := sanitizeExportTitle(long)
	if len(got) != maxExportTitleBytes {
		t.Fatalf("len=%d want %d", len(got), maxExportTitleBytes)
	}

	// Multi-byte runes must not be split.
	multi := strings.Repeat("é", maxExportTitleBytes)
	got = sanitizeExportTitle(multi)
	if len(got) > maxExportTitleBytes {
		t.Fatalf("len=%d exceeds %d", len(got), maxExportTitleBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated title is not valid UTF-8")
	}
	if got == "" {
		t.Fatal("expected non-empty truncated title")
	}

	// Entry with longest sidecar suffix stays under 255 bytes.
	id := strings.Repeat("x", 15)
	base := exportEntryBase(id, strings.Repeat("t", 300))
	metaName := base + ".metadata.json"
	if len(metaName) > 255 {
		t.Fatalf("metadata entry name len=%d exceeds 255: %q", len(metaName), metaName)
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
