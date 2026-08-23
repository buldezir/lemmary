package pdftool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// trailerFile builds a byte string whose startxref points at the xref keyword,
// so the trailer that follows is genuinely past the cross-reference table.
func trailerFile(trailer string) []byte {
	head := "%PDF-1.4\n1 0 obj\n<< /Type /Catalog >>\nendobj\n"
	xrefAt := len(head)
	return []byte(fmt.Sprintf("%sxref\n0 1\n%s\nstartxref\n%d\n%%%%EOF\n", head, trailer, xrefAt))
}

func TestCanonicalizeTrailerIDReplacesTheArray(t *testing.T) {
	t.Parallel()

	original := trailerFile("trailer\n<</Size 32 /ID [(abc) (d\\)ef)] /Root 1 0 R >>")
	out, changed := canonicalizeTrailerID(original)

	if !changed {
		t.Fatal("expected the ID to be rewritten")
	}
	if !bytes.Contains(out, canonicalID) {
		t.Fatalf("canonical ID missing from %q", out)
	}
	if bytes.Contains(out, []byte("(abc)")) {
		t.Fatalf("the random ID survived: %q", out)
	}
	if !bytes.Contains(out, []byte("/Root 1 0 R")) {
		t.Fatalf("the rest of the trailer was damaged: %q", out)
	}
}

func TestCanonicalizeTrailerIDIsIdempotent(t *testing.T) {
	t.Parallel()

	once, _ := canonicalizeTrailerID(trailerFile("trailer\n<</Size 4 /ID [(abcd) (efgh)] >>"))
	twice, changed := canonicalizeTrailerID(once)
	if changed {
		t.Fatal("a canonical file must not be rewritten again")
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("second pass changed the bytes: %q vs %q", once, twice)
	}
}

func TestCanonicalizeTrailerIDRefusesUnsafeShapes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		data []byte
	}{
		{"no id", trailerFile("trailer\n<</Size 4 /Root 1 0 R >>")},
		{"no startxref", []byte("<</ID [(abcd) (efgh)] >>")},
		{"id is not an array", trailerFile("trailer\n<</ID /Broken >>")},
		{"unterminated array", trailerFile("trailer\n<</ID [(abcd)")},
		{
			// An /ID that sits before the xref table cannot be resized without
			// shifting an offset something points at.
			name: "id before the xref table",
			data: []byte("%PDF-1.4\n1 0 obj\n<< /ID [(abcd) (efgh)] >>\nendobj\nxref\n0 1\ntrailer\n<< >>\nstartxref\n45\n%%EOF\n"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := canonicalizeTrailerID(tc.data)
			if changed {
				t.Fatalf("unexpected rewrite: %q", out)
			}
			if !bytes.Equal(out, tc.data) {
				t.Fatalf("bytes changed: %q", out)
			}
		})
	}
}

func TestCanonicalizeTrailerIDIgnoresBracketsInsideStrings(t *testing.T) {
	t.Parallel()

	original := trailerFile("trailer\n<</ID [(a]b) (c\\)d)] /Root 1 0 R >>")
	out, changed := canonicalizeTrailerID(original)
	if !changed {
		t.Fatal("expected a rewrite")
	}
	if !bytes.Contains(out, []byte("/Root 1 0 R")) {
		t.Fatalf("the array end was misplaced: %q", out)
	}
	if bytes.Contains(out, []byte("a]b")) {
		t.Fatalf("the random ID survived: %q", out)
	}
}

func TestExtractRangeIsByteStableAcrossRuns(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdfseparate", "pdfunite")

	source := writePDF(t, 6)
	dir := t.TempDir()

	digest := func(name string, from, to int) [32]byte {
		path := filepath.Join(dir, name)
		if err := ExtractRange(context.Background(), source, from, to, path); err != nil {
			t.Fatalf("ExtractRange() error: %v", err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return sha256.Sum256(data)
	}

	// Two extractions of the same pages must hash equal, or the app's
	// exact-duplicate check can never recognize a re-split part.
	if digest("single-a.pdf", 3, 3) != digest("single-b.pdf", 3, 3) {
		t.Fatal("single-page extraction is not byte stable")
	}
	if digest("range-a.pdf", 2, 4) != digest("range-b.pdf", 2, 4) {
		t.Fatal("multi-page extraction is not byte stable")
	}
	// Different page ranges must still differ, or unrelated parts would collide.
	if digest("range-c.pdf", 2, 4) == digest("range-d.pdf", 3, 5) {
		t.Fatal("different page ranges produced identical bytes")
	}
}

func TestExtractRangeOutputStaysReadable(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdfseparate", "pdfunite", "pdftotext")

	source := writePDF(t, 5, "Invoice INV-1001")
	outPath := filepath.Join(t.TempDir(), "part.pdf")
	if err := ExtractRange(context.Background(), source, 2, 3, outPath); err != nil {
		t.Fatalf("ExtractRange() error: %v", err)
	}

	// The canonicalized file must still parse and still carry its text layer.
	if count, err := PageCount(context.Background(), outPath); err != nil || count != 2 {
		t.Fatalf("PageCount()=%d err=%v", count, err)
	}
	text, err := PageText(context.Background(), outPath, 1)
	if err != nil {
		t.Fatalf("PageText() error: %v", err)
	}
	if !strings.Contains(text, "Page 2") || !strings.Contains(text, "Invoice INV-1001") {
		t.Fatalf("text layer damaged: %q", text)
	}
}
