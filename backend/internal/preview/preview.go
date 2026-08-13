package preview

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/tools/filesystem"
)

const (
	// MaxEdge is the longest edge of generated preview images in pixels.
	MaxEdge         = 400
	pdftoppmTimeout = 30 * time.Second
)

// GenerateFirstPagePNG renders the first page of a PDF to a small PNG preview.
func GenerateFirstPagePNG(pdfPath string) (*filesystem.File, error) {
	if strings.ToLower(filepath.Ext(pdfPath)) != ".pdf" {
		return nil, fmt.Errorf("preview: not a PDF file")
	}

	if _, err := exec.LookPath("pdftoppm"); err != nil {
		return nil, fmt.Errorf("preview: pdftoppm not found (install poppler-utils): %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "paperless-preview-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	outPrefix := filepath.Join(tmpDir, "preview")
	ctx, cancel := context.WithTimeout(context.Background(), pdftoppmTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pdftoppm",
		"-png",
		"-f", "1",
		"-l", "1",
		"-scale-to", fmt.Sprintf("%d", MaxEdge),
		"-singlefile",
		pdfPath,
		outPrefix,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("preview: pdftoppm: %w: %s", err, strings.TrimSpace(string(output)))
	}

	previewPath := outPrefix + ".png"
	data, err := os.ReadFile(previewPath)
	if err != nil {
		return nil, fmt.Errorf("preview: read output: %w", err)
	}

	return filesystem.NewFileFromBytes(data, "preview.png")
}
