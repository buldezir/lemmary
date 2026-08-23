package preview

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pocketbase/pocketbase/tools/filesystem"
	"paperless-go/backend/internal/pdftool"
)

const (
	// MaxEdge is the longest edge of generated preview images in pixels.
	MaxEdge         = 400
	pdftoppmTimeout = 30 * time.Second
)

// GenerateFirstPagePNG renders the first page of a PDF to a small PNG preview.
func GenerateFirstPagePNG(pdfPath string) (*filesystem.File, error) {
	if err := pdftool.RequirePDF(pdfPath); err != nil {
		return nil, fmt.Errorf("preview: not a PDF file")
	}

	tmpDir, err := os.MkdirTemp("", "paperless-preview-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	previewPath := filepath.Join(tmpDir, "preview.png")
	ctx, cancel := context.WithTimeout(context.Background(), pdftoppmTimeout)
	defer cancel()

	if err := pdftool.RenderPage(ctx, pdfPath, previewPath, MaxEdge, 1); err != nil {
		return nil, fmt.Errorf("preview: %w", err)
	}

	data, err := os.ReadFile(previewPath)
	if err != nil {
		return nil, fmt.Errorf("preview: read output: %w", err)
	}

	return filesystem.NewFileFromBytes(data, "preview.png")
}
