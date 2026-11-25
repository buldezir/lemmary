package preview

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"lemmary/backend/internal/pdftool/testpdf"
)

func TestGenerateFirstPagePNGRejectsNonPDF(t *testing.T) {
	t.Parallel()

	_, err := GenerateFirstPagePNG(t.Context(), "document.txt")
	if err == nil {
		t.Fatal("expected error for non-PDF input")
	}
}

func TestGenerateFirstPagePNG(t *testing.T) {
	pdfPath := filepath.Join(t.TempDir(), "document.pdf")
	if err := os.WriteFile(pdfPath, testpdf.Multipage(3), 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}

	file, err := GenerateFirstPagePNG(t.Context(), pdfPath)
	if err != nil {
		t.Fatalf("GenerateFirstPagePNG() error: %v", err)
	}
	if file == nil || file.Name == "" {
		t.Fatal("expected a named preview file")
	}
	reader, err := file.Reader.Open()
	if err != nil {
		t.Fatalf("open preview: %v", err)
	}
	defer reader.Close()
	img, err := png.Decode(reader)
	if err != nil {
		t.Fatalf("preview is not a PNG: %v", err)
	}
	if bounds := img.Bounds(); max(bounds.Dx(), bounds.Dy()) != MaxEdge {
		t.Fatalf("preview is %dx%d, want the longest edge at %d", bounds.Dx(), bounds.Dy(), MaxEdge)
	}
}
