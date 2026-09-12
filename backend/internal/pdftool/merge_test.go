package pdftool

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeConcatenatesInOrder(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdfunite", "pdftotext")

	first := writePDF(t, 2, "FIRST")
	second := writePDF(t, 3, "SECOND")
	out := filepath.Join(t.TempDir(), "merged.pdf")

	if err := Merge(context.Background(), out, first, second); err != nil {
		t.Fatalf("Merge() error: %v", err)
	}
	count, err := PageCount(context.Background(), out)
	if err != nil {
		t.Fatalf("PageCount() error: %v", err)
	}
	if count != 5 {
		t.Fatalf("pages=%d want 5", count)
	}
	// Page 1 has to come from the first input: a scan whose pages arrive in the
	// wrong order is a document nobody can read.
	text, err := PageText(context.Background(), out, 1)
	if err != nil {
		t.Fatalf("PageText() error: %v", err)
	}
	if !strings.Contains(text, "FIRST") {
		t.Fatalf("first page text=%q want the first input", text)
	}
}

// The scan staging area appends by merging a new page into the file it already
// holds, so pdfunite's refusal to write over its own input has to be handled
// inside Merge rather than by every caller.
func TestMergeCanWriteOverAnInput(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdfunite")

	staged := writePDF(t, 1)
	page := writePDF(t, 1)

	if err := Merge(context.Background(), staged, staged, page); err != nil {
		t.Fatalf("Merge() error: %v", err)
	}
	count, err := PageCount(context.Background(), staged)
	if err != nil {
		t.Fatalf("PageCount() error: %v", err)
	}
	if count != 2 {
		t.Fatalf("pages=%d want 2", count)
	}
}

func TestMergeIsByteStableAcrossRuns(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdfunite")

	first := writePDF(t, 2)
	second := writePDF(t, 2)
	dir := t.TempDir()

	digest := func(name string) [32]byte {
		out := filepath.Join(dir, name)
		if err := Merge(context.Background(), out, first, second); err != nil {
			t.Fatalf("Merge() error: %v", err)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return sha256.Sum256(data)
	}

	// Without canonicalizeFileID, pdfunite's random trailer /ID would make the
	// same pages hash differently every run and duplicate detection would never
	// recognize a scan it has already seen.
	if digest("a.pdf") != digest("b.pdf") {
		t.Fatal("merge is not byte stable")
	}
}

func TestMergeRefusesFewerThanTwoInputs(t *testing.T) {
	t.Parallel()

	if err := Merge(context.Background(), "out.pdf", "only.pdf"); err == nil {
		t.Fatal("expected an error for a single input")
	}
}
