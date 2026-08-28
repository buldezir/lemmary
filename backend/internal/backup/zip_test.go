package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestWriteArchiveShape(t *testing.T) {
	archive := Archive{
		ExportedAt: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		Taxonomy: Taxonomy{
			Tags:           []string{"finance", "unused"},
			Correspondents: []NamedEntity{{Name: "Acme", NameOriginal: "ACME GmbH"}},
		},
		Documents: []Document{
			{
				ID:               "doc1",
				Title:            "Invoice",
				OriginalFilename: "invoice.pdf",
				OpenFile:         readerOf("PDFBYTES"),
				OpenPreview:      readerOf("PNGBYTES"),
				OCRText:          "Invoice OCR body",
				Metadata:         map[string]any{"id": "doc1", "title": "Invoice", "original_filename": "invoice.pdf"},
			},
			{
				// No OCR text and no preview: both sidecars must be omitted.
				ID:               "doc2",
				Title:            "Note",
				OriginalFilename: "note.txt",
				OpenFile:         readerOf("note body"),
				Metadata:         map[string]any{"id": "doc2", "title": "Note"},
			},
		},
	}

	entries := mustWrite(t, archive)

	assertHas(t, entries, "lemmary-export/[doc1] Invoice.pdf", "PDFBYTES")
	assertHas(t, entries, "lemmary-export/[doc1] Invoice.ocr.txt", "Invoice OCR body")
	assertHas(t, entries, "lemmary-export/[doc1] Invoice.metadata.json", `"title": "Invoice"`)
	assertHas(t, entries, "lemmary-export/[doc1] Invoice.preview.png", "PNGBYTES")
	assertHas(t, entries, "lemmary-export/[doc2] Note.txt", "note body")
	assertHas(t, entries, "lemmary-export/[doc2] Note.metadata.json", "")
	assertMissing(t, entries, "lemmary-export/[doc2] Note.ocr.txt")
	assertMissing(t, entries, "lemmary-export/[doc2] Note.preview.png")

	manifest := mustManifest(t, entries)
	if manifest.Format != Format || manifest.Version != Version {
		t.Fatalf("format=%q version=%d", manifest.Format, manifest.Version)
	}
	if manifest.ExportedAt != "2026-03-01T12:00:00Z" {
		t.Fatalf("exported_at=%q", manifest.ExportedAt)
	}
	if manifest.DocumentCount != 2 || len(manifest.Documents) != 2 {
		t.Fatalf("document_count=%d documents=%d", manifest.DocumentCount, len(manifest.Documents))
	}
	// The taxonomy carries a tag no document references; that is the only place
	// such a tag exists, so losing it here loses it from the restore.
	if len(manifest.Taxonomy.Tags) != 2 || manifest.Taxonomy.Tags[1] != "unused" {
		t.Fatalf("tags=%#v", manifest.Taxonomy.Tags)
	}
	if len(manifest.Taxonomy.DocumentTypes) != 0 {
		t.Fatalf("document_types should be an empty array, got %#v", manifest.Taxonomy.DocumentTypes)
	}

	first := manifest.Documents[0]
	if first.File != "lemmary-export/[doc1] Invoice.pdf" || first.OCR == "" || first.Metadata == "" || first.Preview == "" {
		t.Fatalf("manifest entry %#v", first)
	}
	second := manifest.Documents[1]
	if second.OCR != "" || second.Preview != "" {
		t.Fatalf("expected no sidecars for doc2, got %#v", second)
	}

	// Every path the manifest names must actually be in the archive.
	for _, doc := range manifest.Documents {
		for _, name := range []string{doc.File, doc.OCR, doc.Metadata, doc.Preview} {
			if name == "" {
				continue
			}
			if _, ok := entries[name]; !ok {
				t.Fatalf("manifest names %q which is not in the archive", name)
			}
		}
	}
}

func TestWriteSkipsUnreadableDocument(t *testing.T) {
	archive := Archive{Documents: []Document{
		{
			ID:               "missing",
			Title:            "Gone",
			OriginalFilename: "gone.pdf",
			OpenFile:         func() (io.ReadCloser, error) { return nil, io.ErrUnexpectedEOF },
		},
		{
			ID:               "ok",
			Title:            "Ok",
			OriginalFilename: "ok.txt",
			OpenFile:         readerOf("ok"),
		},
	}}

	entries := mustWrite(t, archive)
	assertMissing(t, entries, "lemmary-export/[missing] Gone.pdf")
	assertHas(t, entries, "lemmary-export/[ok] Ok.txt", "ok")

	// A blob missing from storage must not leave the manifest claiming it.
	manifest := mustManifest(t, entries)
	if manifest.DocumentCount != 1 || manifest.Documents[0].ID != "ok" {
		t.Fatalf("manifest %#v", manifest.Documents)
	}
}

func TestWriteSkipsUnreadablePreviewOnly(t *testing.T) {
	archive := Archive{Documents: []Document{{
		ID:               "doc",
		Title:            "Doc",
		OriginalFilename: "doc.pdf",
		OpenFile:         readerOf("bytes"),
		OpenPreview:      func() (io.ReadCloser, error) { return nil, io.ErrUnexpectedEOF },
	}}}

	entries := mustWrite(t, archive)
	assertHas(t, entries, "lemmary-export/[doc] Doc.pdf", "bytes")
	assertMissing(t, entries, "lemmary-export/[doc] Doc.preview.png")

	manifest := mustManifest(t, entries)
	if manifest.DocumentCount != 1 {
		t.Fatalf("a document must survive a missing thumbnail, got %#v", manifest.Documents)
	}
	if manifest.Documents[0].Preview != "" {
		t.Fatalf("preview=%q", manifest.Documents[0].Preview)
	}
}

func TestEntryBaseRoundTrip(t *testing.T) {
	if got := EntryBase("abc", "Invoice / Q1"); got != "[abc] Invoice Q1" {
		t.Fatalf("got %q", got)
	}
	if got := EntryBase("abc", "  "); got != "[abc] Untitled" {
		t.Fatalf("got %q", got)
	}
	if got := SanitizeTitle(`bad:name*?<>|`); got != "badname" {
		t.Fatalf("got %q", got)
	}

	id, title, ok := ParseEntryBase(EntryBase("abc123", "Invoice Q1"))
	if !ok || id != "abc123" || title != "Invoice Q1" {
		t.Fatalf("id=%q title=%q ok=%v", id, title, ok)
	}
	for _, bad := range []string{"", "no prefix", "[unclosed", "[] empty"} {
		if _, _, ok := ParseEntryBase(bad); ok {
			t.Fatalf("ParseEntryBase(%q) should not parse", bad)
		}
	}
}

func TestSanitizeTitleTruncates(t *testing.T) {
	long := strings.Repeat("a", maxTitleBytes+50)
	got := SanitizeTitle(long)
	if len(got) != maxTitleBytes {
		t.Fatalf("len=%d want %d", len(got), maxTitleBytes)
	}

	// Multi-byte runes must not be split.
	multi := strings.Repeat("é", maxTitleBytes)
	got = SanitizeTitle(multi)
	if len(got) > maxTitleBytes {
		t.Fatalf("len=%d exceeds %d", len(got), maxTitleBytes)
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncated title is not valid UTF-8")
	}
	if got == "" {
		t.Fatal("expected non-empty truncated title")
	}

	// Entry with the longest sidecar suffix stays under 255 bytes.
	metaName := EntryBase(strings.Repeat("x", 15), strings.Repeat("t", 300)) + MetadataSuffix
	if len(metaName) > 255 {
		t.Fatalf("metadata entry name len=%d exceeds 255: %q", len(metaName), metaName)
	}
}

func readerOf(body string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(body)), nil }
}

func mustWrite(t *testing.T, archive Archive) map[string]string {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, archive); err != nil {
		t.Fatalf("Write: %v", err)
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

func mustManifest(t *testing.T, entries map[string]string) Manifest {
	t.Helper()
	raw, ok := entries[ManifestName]
	if !ok {
		t.Fatalf("archive has no manifest; entries %#v", keys(entries))
	}
	var manifest Manifest
	if err := json.Unmarshal([]byte(raw), &manifest); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return manifest
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
