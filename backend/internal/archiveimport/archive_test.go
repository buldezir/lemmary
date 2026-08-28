package archiveimport

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"lemmary/backend/internal/backup"
	"lemmary/backend/internal/duplicates"
)

type zipEntry struct {
	name string
	body string
}

func buildZip(t *testing.T, entries ...zipEntry) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, entry := range entries {
		f, err := w.Create(entry.name)
		if err != nil {
			t.Fatalf("create %s: %v", entry.name, err)
		}
		if _, err := f.Write([]byte(entry.body)); err != nil {
			t.Fatalf("write %s: %v", entry.name, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	return zr
}

func noDuplicates(string) (string, error) { return "", nil }

func manifestJSON(t *testing.T, manifest backup.Manifest) string {
	t.Helper()
	manifest.Format = backup.Format
	manifest.Version = backup.Version
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return string(data)
}

// A backup as the exporter writes it: two documents, one with every sidecar,
// one with none, plus a tag no document references.
func backupArchive(t *testing.T) (*zip.Reader, *backup.Manifest) {
	t.Helper()
	manifest := backup.Manifest{
		DocumentCount: 2,
		Taxonomy: backup.Taxonomy{
			Tags:           []string{"finance", "unused"},
			Correspondents: []backup.NamedEntity{{Name: "Acme"}},
		},
		Documents: []backup.ManifestDocument{
			{
				ID:       "doc1",
				File:     "lemmary-export/[doc1] Invoice.pdf",
				OCR:      "lemmary-export/[doc1] Invoice.ocr.txt",
				Metadata: "lemmary-export/[doc1] Invoice.metadata.json",
				Preview:  "lemmary-export/[doc1] Invoice.preview.png",
			},
			{ID: "doc2", File: "lemmary-export/[doc2] Note.txt"},
		},
	}
	meta := map[string]any{"id": "doc1", "title": "Invoice", "original_filename": "acme-invoice-2026.pdf"}
	metaJSON, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}

	zr := buildZip(t,
		zipEntry{backup.ManifestName, manifestJSON(t, manifest)},
		zipEntry{"lemmary-export/[doc1] Invoice.pdf", "%PDF-invoice"},
		zipEntry{"lemmary-export/[doc1] Invoice.ocr.txt", "invoice text"},
		zipEntry{"lemmary-export/[doc1] Invoice.metadata.json", string(metaJSON)},
		zipEntry{"lemmary-export/[doc1] Invoice.preview.png", "pngbytes"},
		zipEntry{"lemmary-export/[doc2] Note.txt", "note body"},
	)
	parsed, err := backup.ReadManifest(zr)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}
	return zr, parsed
}

func TestScanDescribesBackup(t *testing.T) {
	zr, manifest := backupArchive(t)
	entries, taxonomy, ignored, err := scan(noDuplicates, zr, manifest)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries=%d: %+v", len(entries), entries)
	}
	if ignored != 0 {
		t.Fatalf("ignored=%d", ignored)
	}
	if taxonomy.Count() != 3 {
		t.Fatalf("taxonomy=%#v", taxonomy)
	}

	first := entries[0]
	if first.DocumentID != "doc1" || first.Title != "Invoice" {
		t.Fatalf("entry %#v", first)
	}
	// The name the document was uploaded under, not the export entry name.
	if first.Name != "acme-invoice-2026.pdf" {
		t.Fatalf("name=%q", first.Name)
	}
	if !first.HasOCR || !first.HasMetadata || !first.HasPreview {
		t.Fatalf("sidecars %#v", first)
	}
	if first.Size != int64(len("%PDF-invoice")) || first.checksum == "" {
		t.Fatalf("size=%d checksum=%q", first.Size, first.checksum)
	}

	// Without a metadata sidecar the entry name is all there is to go on.
	if entries[1].Name != "Note.txt" {
		t.Fatalf("name=%q", entries[1].Name)
	}
	if entries[1].HasOCR || entries[1].HasMetadata || entries[1].HasPreview {
		t.Fatalf("entry %#v", entries[1])
	}
}

func TestScanMarksExistingAndRepeatedDocuments(t *testing.T) {
	// doc1 and doc3 hold identical bytes; doc2 is already in the library.
	manifest := backup.Manifest{Documents: []backup.ManifestDocument{
		{ID: "doc1", File: "lemmary-export/[doc1] A.pdf"},
		{ID: "doc2", File: "lemmary-export/[doc2] B.pdf"},
		{ID: "doc3", File: "lemmary-export/[doc3] C.pdf"},
	}}
	zr := buildZip(t,
		zipEntry{backup.ManifestName, manifestJSON(t, manifest)},
		zipEntry{"lemmary-export/[doc1] A.pdf", "same bytes"},
		zipEntry{"lemmary-export/[doc2] B.pdf", "already here"},
		zipEntry{"lemmary-export/[doc3] C.pdf", "same bytes"},
	)
	parsed, err := backup.ReadManifest(zr)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	lookup := func(checksum string) (string, error) {
		// Only the checksum of "already here" resolves to an existing document.
		if checksum == sha256Of(t, "already here") {
			return "existing123", nil
		}
		return "", nil
	}

	entries, _, _, err := scan(lookup, zr, parsed)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if entries[0].Duplicate {
		t.Fatalf("first copy should import: %#v", entries[0])
	}
	if !entries[1].Duplicate || entries[1].DuplicateOf != "existing123" {
		t.Fatalf("entry %#v", entries[1])
	}
	// A repeat inside the archive is a duplicate with no existing id.
	if !entries[2].Duplicate || entries[2].DuplicateOf != "" {
		t.Fatalf("entry %#v", entries[2])
	}
}

func TestScanFlagsMissingAndOversized(t *testing.T) {
	original := maxEntryBytes
	maxEntryBytes = 8
	defer func() { maxEntryBytes = original }()

	manifest := backup.Manifest{Documents: []backup.ManifestDocument{
		{ID: "big", File: "lemmary-export/[big] Big.pdf"},
		{ID: "gone", File: "lemmary-export/[gone] Gone.pdf"},
	}}
	zr := buildZip(t,
		zipEntry{backup.ManifestName, manifestJSON(t, manifest)},
		zipEntry{"lemmary-export/[big] Big.pdf", strings.Repeat("x", 64)},
	)
	parsed, err := backup.ReadManifest(zr)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	entries, _, _, err := scan(noDuplicates, zr, parsed)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !entries[0].Oversized {
		t.Fatalf("entry %#v", entries[0])
	}
	// The manifest names a file the archive does not hold: reported, not fatal.
	if !entries[1].Missing || entries[1].Name != "Gone.pdf" {
		t.Fatalf("entry %#v", entries[1])
	}
}

func TestScanRejectsDenseArchive(t *testing.T) {
	original := maxTotalScanBytes
	maxTotalScanBytes = 16
	defer func() { maxTotalScanBytes = original }()

	manifest := backup.Manifest{Documents: []backup.ManifestDocument{
		{ID: "a", File: "lemmary-export/[a] A.pdf"},
		{ID: "b", File: "lemmary-export/[b] B.pdf"},
	}}
	zr := buildZip(t,
		zipEntry{backup.ManifestName, manifestJSON(t, manifest)},
		zipEntry{"lemmary-export/[a] A.pdf", strings.Repeat("a", 12)},
		zipEntry{"lemmary-export/[b] B.pdf", strings.Repeat("b", 12)},
	)
	parsed, _ := backup.ReadManifest(zr)

	if _, _, _, err := scan(noDuplicates, zr, parsed); !errors.Is(err, ErrArchiveTooDense) {
		t.Fatalf("err=%v want ErrArchiveTooDense", err)
	}
}

// Sidecars are read during the scan too, so they must draw on the same budget
// as the originals; otherwise a pile of high-ratio metadata entries walks past
// the zip-bomb guard.
func TestScanCountsSidecarsAgainstTheBudget(t *testing.T) {
	original := maxTotalScanBytes
	maxTotalScanBytes = 24
	defer func() { maxTotalScanBytes = original }()

	manifest := backup.Manifest{Documents: []backup.ManifestDocument{{
		ID:       "a",
		File:     "lemmary-export/[a] A.pdf",
		Metadata: "lemmary-export/[a] A.metadata.json",
	}}}
	zr := buildZip(t,
		zipEntry{backup.ManifestName, manifestJSON(t, manifest)},
		zipEntry{"lemmary-export/[a] A.pdf", strings.Repeat("a", 12)},
		zipEntry{"lemmary-export/[a] A.metadata.json", `{"original_filename":"` + strings.Repeat("n", 40) + `.pdf"}`},
	)
	parsed, _ := backup.ReadManifest(zr)

	if _, _, _, err := scan(noDuplicates, zr, parsed); !errors.Is(err, ErrArchiveTooDense) {
		t.Fatalf("err=%v want ErrArchiveTooDense", err)
	}
}

func TestScanRejectsEmptyArchive(t *testing.T) {
	zr := buildZip(t, zipEntry{"notes.txt", "nothing to restore"})
	if _, _, _, err := scan(noDuplicates, zr, nil); !errors.Is(err, ErrNoDocuments) {
		t.Fatalf("err=%v want ErrNoDocuments", err)
	}
}

// An archive from before manifests existed still restores, from its names alone.
func TestScanReadsLegacyArchive(t *testing.T) {
	zr := buildZip(t,
		zipEntry{"lemmary-export/[old1] Receipt.pdf", "%PDF-old"},
		zipEntry{"lemmary-export/[old1] Receipt.ocr.txt", "old text"},
		zipEntry{"lemmary-export/[old1] Receipt.metadata.json", `{"title":"Receipt","original_filename":"receipt.pdf"}`},
	)
	entries, taxonomy, ignored, err := scan(noDuplicates, zr, nil)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 1 || entries[0].DocumentID != "old1" || entries[0].Name != "receipt.pdf" {
		t.Fatalf("entries=%#v", entries)
	}
	if !entries[0].HasOCR || !entries[0].HasMetadata {
		t.Fatalf("entry %#v", entries[0])
	}
	// A legacy archive has no manifest, so orphan taxonomy simply is not there.
	if taxonomy.Count() != 0 || ignored != 0 {
		t.Fatalf("taxonomy=%#v ignored=%d", taxonomy, ignored)
	}
}

// A metadata sidecar comes from another instance; a traversal in its stored
// file name must not become a path.
func TestRestoredNameIsASinglePathElement(t *testing.T) {
	manifest := backup.Manifest{Documents: []backup.ManifestDocument{{
		ID:       "doc",
		File:     "lemmary-export/[doc] Doc.pdf",
		Metadata: "lemmary-export/[doc] Doc.metadata.json",
	}}}
	zr := buildZip(t,
		zipEntry{backup.ManifestName, manifestJSON(t, manifest)},
		zipEntry{"lemmary-export/[doc] Doc.pdf", "%PDF"},
		zipEntry{"lemmary-export/[doc] Doc.metadata.json", `{"original_filename":"../../etc/passwd"}`},
	)
	parsed, _ := backup.ReadManifest(zr)

	entries, _, _, err := scan(noDuplicates, zr, parsed)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if strings.Contains(entries[0].Name, "/") || entries[0].Name != "passwd" {
		t.Fatalf("name=%q", entries[0].Name)
	}
}

func TestParseMode(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"", ModeRestore},
		{"restore", ModeRestore},
		{"RESTORE", ModeRestore},
		{"reprocess", ModeReprocess},
	} {
		got, err := ParseMode(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("ParseMode(%q)=%q, %v", tc.in, got, err)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Fatal("expected an error for an unknown mode")
	}
}

func sha256Of(t *testing.T, body string) string {
	t.Helper()
	sum, err := duplicates.SHA256Reader(strings.NewReader(body))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	return sum
}

// A backup of a library that has taxonomy but no documents is still a backup:
// the manifest carries tags that live nowhere else, so it has to be restorable.
func TestScanAcceptsTaxonomyOnlyArchive(t *testing.T) {
	manifest := backup.Manifest{
		Taxonomy: backup.Taxonomy{Tags: []string{"kept", "also-kept"}},
	}
	zr := buildZip(t, zipEntry{backup.ManifestName, manifestJSON(t, manifest)})
	parsed, err := backup.ReadManifest(zr)
	if err != nil {
		t.Fatalf("ReadManifest: %v", err)
	}

	entries, taxonomy, _, err := scan(noDuplicates, zr, parsed)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries=%#v", entries)
	}
	if taxonomy.Count() != 2 {
		t.Fatalf("taxonomy=%#v", taxonomy)
	}

	// With neither documents nor taxonomy there is genuinely nothing to do.
	empty := buildZip(t, zipEntry{backup.ManifestName, manifestJSON(t, backup.Manifest{})})
	emptyManifest, _ := backup.ReadManifest(empty)
	if _, _, _, err := scan(noDuplicates, empty, emptyManifest); !errors.Is(err, ErrNoDocuments) {
		t.Fatalf("err=%v want ErrNoDocuments", err)
	}
}
