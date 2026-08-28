package backup

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"sort"
	"testing"
)

func TestReadManifestAbsent(t *testing.T) {
	zr := zipOf(t, map[string]string{"lemmary-export/[a] A.pdf": "x"})
	manifest, err := ReadManifest(zr)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	if manifest != nil {
		t.Fatal("an archive without a manifest must read as nil, not an error")
	}
}

func TestReadManifestRejectsNewerVersion(t *testing.T) {
	zr := zipOf(t, map[string]string{
		ManifestName: mustJSON(t, Manifest{Format: Format, Version: Version + 1}),
	})
	if _, err := ReadManifest(zr); !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("err=%v want ErrUnsupportedVersion", err)
	}
}

func TestReadManifestRejectsCorrupt(t *testing.T) {
	// A present-but-broken manifest must not silently fall back to sniffing:
	// the archive is corrupt, and guessing would restore a subtly wrong library.
	zr := zipOf(t, map[string]string{ManifestName: "{not json"})
	if _, err := ReadManifest(zr); err == nil {
		t.Fatal("expected an error for a corrupt manifest")
	}

	zr = zipOf(t, map[string]string{ManifestName: mustJSON(t, Manifest{Format: "something-else", Version: 1})})
	if _, err := ReadManifest(zr); err == nil {
		t.Fatal("expected an error for a foreign archive format")
	}
}

func TestGroupsFromManifest(t *testing.T) {
	manifest := Manifest{
		Format:  Format,
		Version: Version,
		Documents: []ManifestDocument{
			{
				ID:       "doc1",
				File:     "lemmary-export/[doc1] Invoice.pdf",
				OCR:      "lemmary-export/[doc1] Invoice.ocr.txt",
				Metadata: "lemmary-export/[doc1] Invoice.metadata.json",
				Preview:  "lemmary-export/[doc1] Invoice.preview.png",
			},
			// Names a preview the archive does not hold.
			{ID: "doc2", File: "lemmary-export/[doc2] Note.txt", Preview: "lemmary-export/[doc2] Note.preview.png"},
		},
	}
	zr := zipOf(t, map[string]string{
		ManifestName:                                  mustJSON(t, manifest),
		"lemmary-export/[doc1] Invoice.pdf":           "pdf",
		"lemmary-export/[doc1] Invoice.ocr.txt":       "ocr",
		"lemmary-export/[doc1] Invoice.metadata.json": "{}",
		"lemmary-export/[doc1] Invoice.preview.png":   "png",
		"lemmary-export/[doc2] Note.txt":              "note",
		"lemmary-export/stray.txt":                    "stray",
	})

	parsed, err := ReadManifest(zr)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	groups, ignored := Groups(zr, parsed)

	if len(groups) != 2 {
		t.Fatalf("groups=%d", len(groups))
	}
	if groups[0].ID != "doc1" || groups[0].Title != "Invoice" || groups[0].OCR == "" || groups[0].Preview == "" {
		t.Fatalf("group %#v", groups[0])
	}
	if groups[1].ID != "doc2" || groups[1].OCR != "" {
		t.Fatalf("group %#v", groups[1])
	}
	// A sidecar the manifest names but the archive lacks is simply absent.
	if groups[1].Preview != "" {
		t.Fatalf("preview=%q, want empty for a path not in the archive", groups[1].Preview)
	}
	if ignored != 1 {
		t.Fatalf("ignored=%d want 1 (the stray entry)", ignored)
	}
}

func TestGroupsSniffedWithoutManifest(t *testing.T) {
	zr := zipOf(t, map[string]string{
		"lemmary-export/[doc1] Invoice.pdf":           "pdf",
		"lemmary-export/[doc1] Invoice.ocr.txt":       "ocr",
		"lemmary-export/[doc1] Invoice.metadata.json": "{}",
		"lemmary-export/[doc2] Note.txt":              "note",
		// Sidecars with no original: not a document.
		"lemmary-export/[doc3] Orphan.ocr.txt": "ocr",
		// Outside the export root, and macOS bookkeeping.
		"elsewhere/thing.pdf":            "x",
		"__MACOSX/lemmary-export/._x":    "x",
		"lemmary-export/nested/deep.pdf": "x",
	})

	groups, ignored := Groups(zr, nil)
	if len(groups) != 2 {
		t.Fatalf("groups=%#v", groups)
	}
	if groups[0].ID != "doc1" || groups[0].Title != "Invoice" || groups[0].OCR == "" || groups[0].Metadata == "" {
		t.Fatalf("group %#v", groups[0])
	}
	if groups[1].ID != "doc2" || groups[1].File != "lemmary-export/[doc2] Note.txt" {
		t.Fatalf("group %#v", groups[1])
	}
	// Orphan sidecar + the two entries outside the flat root. The macOS fork is
	// bookkeeping, not an ignored entry.
	if ignored != 3 {
		t.Fatalf("ignored=%d want 3", ignored)
	}
}

// A document whose own file is a .txt named like an OCR sidecar is the one case
// entry names cannot resolve. The manifest is what makes it unambiguous.
func TestManifestResolvesSidecarLookalike(t *testing.T) {
	entries := map[string]string{
		"lemmary-export/[doc1] Report.ocr.txt": "the actual document",
	}

	if groups, _ := Groups(zipOf(t, entries), nil); len(groups) != 0 {
		t.Fatalf("sniffing should read this as a stray sidecar, got %#v", groups)
	}

	manifest := Manifest{
		Format:    Format,
		Version:   Version,
		Documents: []ManifestDocument{{ID: "doc1", File: "lemmary-export/[doc1] Report.ocr.txt"}},
	}
	entries[ManifestName] = mustJSON(t, manifest)
	groups, ignored := Groups(zipOf(t, entries), &manifest)
	if len(groups) != 1 || groups[0].File != "lemmary-export/[doc1] Report.ocr.txt" || groups[0].OCR != "" {
		t.Fatalf("groups=%#v", groups)
	}
	if ignored != 0 {
		t.Fatalf("ignored=%d", ignored)
	}
}

func zipOf(t *testing.T, entries map[string]string) *zip.Reader {
	t.Helper()
	// Written in sorted order so the entry order — and therefore the group
	// order the assertions rely on — does not depend on map iteration.
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		body := entries[name]
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	return zr
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}
