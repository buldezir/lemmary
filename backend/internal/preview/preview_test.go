package preview

import (
	"os"
	"os/exec"
	"testing"
)

func TestGenerateFirstPagePNGRejectsNonPDF(t *testing.T) {
	t.Parallel()

	_, err := GenerateFirstPagePNG(t.Context(), "document.txt")
	if err == nil {
		t.Fatal("expected error for non-PDF input")
	}
}

func TestGenerateFirstPagePNG(t *testing.T) {
	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Skip("pdftoppm not installed")
	}

	pdfPath := os.Getenv("PREVIEW_TEST_PDF")
	if pdfPath == "" {
		t.Skip("set PREVIEW_TEST_PDF to run integration test")
	}

	file, err := GenerateFirstPagePNG(t.Context(), pdfPath)
	if err != nil {
		t.Fatalf("GenerateFirstPagePNG() error: %v", err)
	}
	if file == nil {
		t.Fatal("expected preview file")
	}
	if file.Name == "" {
		t.Fatal("expected preview file name")
	}
}
