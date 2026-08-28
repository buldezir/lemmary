package limits

import (
	"os"
	"os/exec"
	"testing"

	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/pdftool/testpdf"
)

func requirePoppler(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("pdfinfo"); err != nil {
		t.Skip("pdfinfo not installed")
	}
}

func uploadFromBytes(t *testing.T, name string, data []byte) *filesystem.File {
	t.Helper()
	file, err := filesystem.NewFileFromBytes(data, name)
	if err != nil {
		t.Fatalf("build upload: %v", err)
	}
	// NewFileFromBytes rewrites Name with a random suffix; the extension is what
	// PageCountOfUpload dispatches on, and that survives.
	return file
}

func TestPageCountOfUploadCountsPDFPages(t *testing.T) {
	requirePoppler(t)
	for _, pages := range []int{1, 3, 12} {
		file := uploadFromBytes(t, "scan.pdf", testpdf.Multipage(pages))
		if got := PageCountOfUpload(nil, file); got != int64(pages) {
			t.Fatalf("a %d-page PDF counted as %d", pages, got)
		}
	}
}

// Everything that is not a PDF is one document and one page, and must not cost
// a temp file to find that out.
func TestPageCountOfUploadNonPDFIsOnePage(t *testing.T) {
	for _, name := range []string{
		"receipt.png", "receipt.jpg", "receipt.webp",
		"notes.txt", "table.csv",
		"letter.docx", "book.xlsx",
	} {
		file := uploadFromBytes(t, name, []byte("not a pdf"))
		if got := PageCountOfUpload(nil, file); got != SinglePage {
			t.Fatalf("%s counted as %d pages, want %d", name, got, SinglePage)
		}
	}
}

// A file poppler cannot read must still be storable: refusing it here would turn
// an unreadable PDF into data loss, when the processing pipeline is the thing
// that should report it.
func TestPageCountOfUploadUnreadablePDFIsOnePage(t *testing.T) {
	requirePoppler(t)
	file := uploadFromBytes(t, "broken.pdf", []byte("%PDF-1.4 but not really"))
	if got := PageCountOfUpload(nil, file); got != SinglePage {
		t.Fatalf("an unreadable PDF counted as %d pages, want %d", got, SinglePage)
	}
}

func TestPageCountOfUploadNilFile(t *testing.T) {
	if got := PageCountOfUpload(nil, nil); got != SinglePage {
		t.Fatalf("a nil upload counted as %d, want %d", got, SinglePage)
	}
}

// The temp copy a PDF needs must not outlive the call.
func TestPageCountOfUploadRemovesItsTempFile(t *testing.T) {
	requirePoppler(t)
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)

	file := uploadFromBytes(t, "scan.pdf", testpdf.Multipage(2))
	if got := PageCountOfUpload(nil, file); got != 2 {
		t.Fatalf("page count = %d, want 2", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read temp dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("%d temp files left behind: %v", len(entries), entries)
	}
}
