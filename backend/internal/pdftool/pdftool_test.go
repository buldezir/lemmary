package pdftool

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lemmary/backend/internal/pdftool/testpdf"
)

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
	count, err := PageCount(context.Background(), writePDF(t, 7))
	if err != nil {
		t.Fatalf("PageCount() error: %v", err)
	}
	if count != 7 {
		t.Fatalf("expected 7 pages, got %d", count)
	}
}

func TestPageCountRejectsNonPDFContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.pdf")
	if err := os.WriteFile(path, []byte("this is not a pdf"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := PageCount(context.Background(), path); err == nil {
		t.Fatal("expected an error for a file that is not a PDF")
	}
}

func TestRenderPagesNamesEveryPageWithoutPadding(t *testing.T) {
	// 12 pages crosses into two digits, where a padded name (page-01.png)
	// would no longer match PagePNGPath.
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
}

func TestRenderPagesRejectsUnexpectedPageCount(t *testing.T) {
	source := writePDF(t, 3)
	outDir := filepath.Join(t.TempDir(), "thumbs")

	if _, err := RenderPages(context.Background(), source, outDir, 200, 4); err == nil {
		t.Fatal("expected an error when the rendered page count disagrees")
	}
}

func TestRenderPage(t *testing.T) {
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
	// 8..11 crosses the single/double digit boundary, where a lexical order
	// would put page 11 before page 8.
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

func TestAllPagesTextSplitsOnThePageSeparator(t *testing.T) {
	source := writePDF(t, 12, "Invoice INV-1001")
	pages, err := AllPagesText(context.Background(), source, 12)
	if err != nil {
		t.Fatalf("AllPagesText() error: %v", err)
	}
	if len(pages) != 12 {
		t.Fatalf("pages=%d want 12", len(pages))
	}
	for i, text := range pages {
		want := fmt.Sprintf("Page %d", i+1)
		if !strings.Contains(text, want) {
			t.Fatalf("page %d text=%q, want %q", i+1, text, want)
		}
		// One page's text must not spill into the next, which is what the form
		// feed boundary is there for.
		if other := fmt.Sprintf("Page %d", i+2); i+2 <= 12 && strings.Contains(text, other) {
			t.Fatalf("page %d text leaked %q: %q", i+1, other, text)
		}
	}
}

func TestAllPagesTextSquaresUpADisagreeingPageCount(t *testing.T) {
	// Callers index the result by page number, so a file whose text sections do
	// not match its page count must still return one entry per page.
	source := writePDF(t, 3)
	pages, err := AllPagesText(context.Background(), source, 5)
	if err != nil {
		t.Fatalf("AllPagesText() error: %v", err)
	}
	if len(pages) != 5 {
		t.Fatalf("pages=%d want 5", len(pages))
	}
	if pages[4] != "" {
		t.Fatalf("missing page text=%q want empty", pages[4])
	}
}

// A call cut short has its worker killed mid-render, and the next call gets a
// fresh one.
func TestCancelKillsTheWorkerMidCall(t *testing.T) {
	source := writePDF(t, 200)
	outDir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			if _, err := os.Stat(PagePNGPath(outDir, 1)); err == nil {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	_, err := RenderPages(ctx, source, outDir, 3000, 200)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RenderPages() error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(PagePNGPath(outDir, 200)); err == nil {
		t.Fatal("the render ran to the end despite the cancel")
	}
	if count, err := PageCount(context.Background(), writePDF(t, 2)); err != nil || count != 2 {
		t.Fatalf("PageCount() after a kill = %d, %v", count, err)
	}
}

// Only PDFium's verdict on the file counts as unreadable: the unlock step lets
// such a file through for preview to report, and fails on anything else.
func TestUnreadableNamesADamagedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.pdf")
	if err := os.WriteFile(path, []byte("this is not a pdf"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PageCount(context.Background(), path); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("PageCount() error = %v, want ErrUnreadable", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PageCount(ctx, writePDF(t, 2)); err == nil || errors.Is(err, ErrUnreadable) {
		t.Fatalf("PageCount() on a cancelled context = %v, want an error that is not ErrUnreadable", err)
	}
}

// Page text follows the content order, so a two-column page reads column by
// column; pdftotext -layout interleaved the columns line by line. Split
// detection and the extraction prompt read this text, so the order is pinned.
func TestPageTextReadsTwoColumnsOneAfterTheOther(t *testing.T) {
	path := filepath.Join(t.TempDir(), "columns.pdf")
	left := []string{"Invoice INV-1001", "Acme Plumbing GmbH", "Total 120.00 EUR"}
	right := []string{"Customer service", "Mon to Fri 9-17", "help@example.com"}
	if err := os.WriteFile(path, testpdf.TwoColumns(left, right), 0o600); err != nil {
		t.Fatal(err)
	}
	text, err := PageText(context.Background(), path, 1)
	if err != nil {
		t.Fatalf("PageText() error: %v", err)
	}
	last := -1
	for _, line := range append(left, right...) {
		at := strings.Index(text, line)
		if at < 0 || at < last {
			t.Fatalf("%q missing or out of column order in %q", line, text)
		}
		last = at
	}
}

// One page PDFium cannot load leaves its entry empty instead of failing the
// whole document: split detection still gets the other pages.
func TestAllPagesTextSkipsAPageThatDoesNotLoad(t *testing.T) {
	// The second kid points at the font object, which is not a page.
	data := bytes.Replace(testpdf.Multipage(3), []byte("/Kids [4 0 R 6 0 R 8 0 R]"), []byte("/Kids [4 0 R 3 0 R 8 0 R]"), 1)
	path := filepath.Join(t.TempDir(), "broken-page.pdf")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	pages, err := AllPagesText(context.Background(), path, 3)
	if err != nil {
		t.Fatalf("AllPagesText() error: %v", err)
	}
	if !strings.Contains(pages[0], "Page 1") || pages[1] != "" || !strings.Contains(pages[2], "Page 3") {
		t.Fatalf("pages = %q, want page 2 empty and the others read", pages)
	}
}
