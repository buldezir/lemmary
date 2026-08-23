package pdftool

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"paperless-go/backend/internal/pdftool/testpdf"
)

// requirePoppler skips when the poppler binaries a test needs are absent, the
// same guard the preview package uses.
func requirePoppler(t *testing.T, binaries ...string) {
	t.Helper()
	for _, binary := range binaries {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s not installed", binary)
		}
	}
}

// writePDF puts a generated pageCount-page PDF in a temp dir and returns its path.
func writePDF(t *testing.T, pageCount int, extraLines ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "source.pdf")
	if err := os.WriteFile(path, testpdf.Multipage(pageCount, extraLines...), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	return path
}

func TestRequirePDFRejectsOtherExtensions(t *testing.T) {
	t.Parallel()

	if err := RequirePDF("scan.PDF"); err != nil {
		t.Fatalf("uppercase extension should be accepted: %v", err)
	}
	if err := RequirePDF("scan.png"); err == nil {
		t.Fatal("expected an error for a non-PDF path")
	}
}

func TestPageCount(t *testing.T) {
	requirePoppler(t, "pdfinfo")

	count, err := PageCount(context.Background(), writePDF(t, 7))
	if err != nil {
		t.Fatalf("PageCount() error: %v", err)
	}
	if count != 7 {
		t.Fatalf("expected 7 pages, got %d", count)
	}
}

func TestPageCountRejectsNonPDFContent(t *testing.T) {
	requirePoppler(t, "pdfinfo")

	path := filepath.Join(t.TempDir(), "notes.pdf")
	if err := os.WriteFile(path, []byte("this is not a pdf"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := PageCount(context.Background(), path); err == nil {
		t.Fatal("expected an error for a file that is not a PDF")
	}
}

func TestRenderPagesNamesEveryPageWithoutPadding(t *testing.T) {
	requirePoppler(t, "pdftoppm")

	// 12 pages makes pdftoppm zero-pad its own output (raw-01.png), which is
	// exactly the case the renaming exists for.
	source := writePDF(t, 12)
	outDir := filepath.Join(t.TempDir(), "thumbs")

	paths, err := RenderPages(context.Background(), source, outDir, 200, 12)
	if err != nil {
		t.Fatalf("RenderPages() error: %v", err)
	}
	if len(paths) != 12 {
		t.Fatalf("expected 12 rendered pages, got %d", len(paths))
	}
	for page := 1; page <= 12; page++ {
		want := PagePNGPath(outDir, page)
		if paths[page-1] != want {
			t.Fatalf("page %d rendered to %s, want %s", page, paths[page-1], want)
		}
		data, err := os.ReadFile(want)
		if err != nil {
			t.Fatalf("read page %d: %v", page, err)
		}
		if !strings.HasPrefix(string(data), "\x89PNG") {
			t.Fatalf("page %d is not a PNG", page)
		}
	}
	// No padded staging files may be left behind for the sweeper to trip over.
	leftovers, _ := filepath.Glob(filepath.Join(outDir, "raw-*.png"))
	if len(leftovers) != 0 {
		t.Fatalf("staging files left behind: %v", leftovers)
	}
}

func TestRenderPagesRejectsUnexpectedPageCount(t *testing.T) {
	requirePoppler(t, "pdftoppm")

	source := writePDF(t, 3)
	outDir := filepath.Join(t.TempDir(), "thumbs")

	if _, err := RenderPages(context.Background(), source, outDir, 200, 4); err == nil {
		t.Fatal("expected an error when the rendered page count disagrees")
	}
}

func TestRenderPage(t *testing.T) {
	requirePoppler(t, "pdftoppm")

	outPath := filepath.Join(t.TempDir(), "preview.png")
	if err := RenderPage(context.Background(), writePDF(t, 4), outPath, 200, 3); err != nil {
		t.Fatalf("RenderPage() error: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.HasPrefix(string(data), "\x89PNG") {
		t.Fatal("output is not a PNG")
	}
}

func TestPageText(t *testing.T) {
	requirePoppler(t, "pdftotext")

	source := writePDF(t, 5, "Invoice INV-1001")
	text, err := PageText(context.Background(), source, 4)
	if err != nil {
		t.Fatalf("PageText() error: %v", err)
	}
	if !strings.Contains(text, "Page 4") {
		t.Fatalf("expected the page 4 marker, got %q", text)
	}
	if strings.Contains(text, "Page 3") {
		t.Fatalf("page 4 text leaked another page: %q", text)
	}
	if !strings.Contains(text, "Invoice INV-1001") {
		t.Fatalf("expected the invoice line, got %q", text)
	}
}

func TestExtractRangeSinglePage(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdfseparate")

	source := writePDF(t, 6)
	outPath := filepath.Join(t.TempDir(), "part.pdf")
	if err := ExtractRange(context.Background(), source, 2, 2, outPath); err != nil {
		t.Fatalf("ExtractRange() error: %v", err)
	}

	count, err := PageCount(context.Background(), outPath)
	if err != nil {
		t.Fatalf("PageCount() error: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 page, got %d", count)
	}
}

func TestExtractRangeKeepsPageOrder(t *testing.T) {
	requirePoppler(t, "pdfinfo", "pdfseparate", "pdfunite", "pdftotext")

	// 8..11 crosses the single/double digit boundary, so a lexical sort of the
	// pdfseparate output would put page 11 before page 8.
	source := writePDF(t, 12)
	outPath := filepath.Join(t.TempDir(), "part.pdf")
	if err := ExtractRange(context.Background(), source, 8, 11, outPath); err != nil {
		t.Fatalf("ExtractRange() error: %v", err)
	}

	count, err := PageCount(context.Background(), outPath)
	if err != nil {
		t.Fatalf("PageCount() error: %v", err)
	}
	if count != 4 {
		t.Fatalf("expected 4 pages, got %d", count)
	}
	for i, want := range []string{"Page 8", "Page 9", "Page 10", "Page 11"} {
		text, err := PageText(context.Background(), outPath, i+1)
		if err != nil {
			t.Fatalf("PageText(%d) error: %v", i+1, err)
		}
		if !strings.Contains(text, want) {
			t.Fatalf("extracted page %d holds %q, want %q", i+1, text, want)
		}
	}
}

func TestExtractRangeRejectsInvalidRange(t *testing.T) {
	t.Parallel()

	source := filepath.Join(t.TempDir(), "source.pdf")
	if err := ExtractRange(context.Background(), source, 3, 2, source+".out"); err == nil {
		t.Fatal("expected an error when to < from")
	}
	if err := ExtractRange(context.Background(), source, 0, 2, source+".out"); err == nil {
		t.Fatal("expected an error when from < 1")
	}
}
