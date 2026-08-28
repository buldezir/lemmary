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
	// NewFileFromBytes rewrites Name with a random suffix, keeping the extension.
	// That only matters for the tests that assert the extension is *ignored*.
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

// The name is the client's word. Trusting it made every page limit bypassable
// with `mv`: .txt, .csv and .docx are all accepted upload types, so a multi-page
// PDF renamed to one of them was stored as the PDF it is and charged one page.
func TestPageCountOfUploadIgnoresASpoofedExtension(t *testing.T) {
	requirePoppler(t)
	pdf := testpdf.Multipage(7)
	for _, name := range []string{
		"notes.txt", "table.csv", "letter.docx", "book.xlsx", "receipt.png",
		"no-extension-at-all",
	} {
		file := uploadFromBytes(t, name, pdf)
		if got := PageCountOfUpload(nil, file); got != 7 {
			t.Fatalf("a 7-page PDF named %s counted as %d pages", name, got)
		}
	}
}

// The mirror image: something merely *named* .pdf is not charged for pages it
// does not have, and never reaches pdfinfo.
func TestPageCountOfUploadIgnoresAPDFNameWithoutTheHeader(t *testing.T) {
	file := uploadFromBytes(t, "invoice.pdf", []byte("this is plain text, not a PDF"))
	if got := PageCountOfUpload(nil, file); got != SinglePage {
		t.Fatalf("a text file named .pdf counted as %d pages, want %d", got, SinglePage)
	}
}

func TestHasPDFHeader(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		want bool
	}{
		{"real pdf", testpdf.Multipage(1), true},
		{"header only", []byte("%PDF-1.7"), true},
		{"plain text", []byte("hello there"), false},
		// Shorter than the header, so io.ReadFull cannot fill it. (A wholly
		// empty upload cannot be constructed, and PocketBase rejects one.)
		{"truncated", []byte("%PDF"), false},
		// A PDF header that is not at the very start is not one.
		{"offset header", append([]byte("junk"), []byte("%PDF-1.7")...), false},
	} {
		if got := hasPDFHeader(uploadFromBytes(t, tc.name, tc.data)); got != tc.want {
			t.Fatalf("%s: hasPDFHeader = %v, want %v", tc.name, got, tc.want)
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
