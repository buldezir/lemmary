package ocr

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	vision "cloud.google.com/go/vision/v2/apiv1"
	"cloud.google.com/go/vision/v2/apiv1/visionpb"
	"google.golang.org/api/option"
	"lemmary/backend/internal/aiprovider"
)

// The gRPC client is goroutine-safe and must be Close()d to release its
// connection, but providers are rebuilt on every settings reload and OCR-test
// request while older config snapshots may still be mid-job with the previous
// instance — so instead of closing, reuse one client per API key for the
// process lifetime. The map stays as small as the set of keys ever configured.
var (
	visionClientMu sync.Mutex
	visionClients  = map[string]*vision.ImageAnnotatorClient{}
)

func visionClientForKey(apiKey string) (*vision.ImageAnnotatorClient, error) {
	visionClientMu.Lock()
	defer visionClientMu.Unlock()
	if client, ok := visionClients[apiKey]; ok {
		return client, nil
	}
	client, err := vision.NewImageAnnotatorClient(context.Background(), option.WithAPIKey(apiKey))
	if err != nil {
		return nil, err
	}
	visionClients[apiKey] = client
	return client, nil
}

const visionMaxFilePagesPerRequest = 5
const visionAnnotateFilesURL = "https://vision.googleapis.com/v1/files:annotate"
const visionAnnotateImagesURL = "https://vision.googleapis.com/v1/images:annotate"

func visionDocumentTextFeatures() []*visionpb.Feature {
	return []*visionpb.Feature{
		{Type: visionpb.Feature_DOCUMENT_TEXT_DETECTION},
	}
}

type GoogleVisionProvider struct {
	client  *vision.ImageAnnotatorClient
	initErr error
	logger  *slog.Logger
}

func NewGoogleVisionProvider(apiKey string, logger *slog.Logger) *GoogleVisionProvider {
	client, err := visionClientForKey(apiKey)
	if err != nil {
		logger.Error("google vision client init failed", slog.Any("error", err))
	} else {
		logger.Info("google vision client initialized")
	}
	return &GoogleVisionProvider{
		client:  client,
		initErr: err,
		logger:  logger,
	}
}

func (p *GoogleVisionProvider) Name() string {
	return "google_vision"
}

func (p *GoogleVisionProvider) ExtractText(ctx context.Context, filePath string, mimeType string) (string, error) {
	start := time.Now()
	if p.initErr != nil {
		return "", fmt.Errorf("google vision client: %w", p.initErr)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read file for OCR: %w", err)
	}

	effectiveMime := mimeType
	mode := "image"
	if isVisionFileInput(mimeType, filePath) {
		effectiveMime = effectiveMimeType(mimeType, filePath)
		mode = "file"
	}
	p.logger.Info("google vision starting",
		"file", filepath.Base(filePath),
		"mime", mimeType,
		"effective_mime", effectiveMime,
		"mode", mode,
		"bytes", len(data),
	)

	var text string
	if mode == "file" {
		text, err = p.extractFileText(ctx, data, effectiveMime)
	} else {
		text, err = p.extractImageText(ctx, data, mimeType)
	}
	if err != nil {
		p.logger.Error("google vision failed",
			"file", filepath.Base(filePath),
			"duration", time.Since(start).Round(time.Millisecond),
			slog.Any("error", err),
		)
		return "", err
	}
	p.logger.Info("google vision complete",
		"file", filepath.Base(filePath),
		"chars", len(text),
		"duration", time.Since(start).Round(time.Millisecond),
	)
	return text, nil
}

func isVisionFileInput(mimeType, filePath string) bool {
	switch mimeType {
	case "application/pdf", "image/tiff", "image/gif":
		return true
	}

	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".pdf", ".tif", ".tiff", ".gif":
		return true
	default:
		return false
	}
}

func (p *GoogleVisionProvider) extractFileText(ctx context.Context, content []byte, mimeType string) (string, error) {
	first, err := p.annotateFile(ctx, content, mimeType, nil)
	if err != nil {
		return "", err
	}

	parts := append([]string{}, first.pageTexts...)
	totalPages := first.totalPages
	p.logger.Info("google vision file",
		"mime", mimeType,
		"total_pages", totalPages,
		"first_batch_pages", len(first.pageTexts),
	)

	for start := visionMaxFilePagesPerRequest + 1; start <= totalPages; start += visionMaxFilePagesPerRequest {
		end := start + visionMaxFilePagesPerRequest - 1
		if end > totalPages {
			end = totalPages
		}

		pages := make([]int32, 0, end-start+1)
		for page := start; page <= end; page++ {
			pages = append(pages, int32(page))
		}

		p.logger.Info("google vision file batch", "pages_start", start, "pages_end", end)
		batch, err := p.annotateFile(ctx, content, mimeType, pages)
		if err != nil {
			return "", fmt.Errorf("ocr %s pages %d-%d: %w", mimeType, start, end, err)
		}
		parts = append(parts, batch.pageTexts...)
		p.logger.Info("google vision file batch complete",
			"pages_start", start,
			"pages_end", end,
			"extracted", len(batch.pageTexts),
		)
	}

	text := strings.Join(parts, "\n\n")
	if text == "" {
		return "", fmt.Errorf("google vision returned empty text for mime type %s", mimeType)
	}

	return text, nil
}

type fileAnnotateResult struct {
	pageTexts  []string
	totalPages int
}

func (p *GoogleVisionProvider) annotateFile(ctx context.Context, content []byte, mimeType string, pages []int32) (fileAnnotateResult, error) {
	start := time.Now()
	req := &visionpb.BatchAnnotateFilesRequest{
		Requests: []*visionpb.AnnotateFileRequest{
			{
				InputConfig: &visionpb.InputConfig{
					Content:  content,
					MimeType: mimeType,
				},
				Features: visionDocumentTextFeatures(),
				Pages:    pages,
			},
		},
	}

	aiprovider.LogRequest(p.logger, aiprovider.SDKGoogleVision, "POST", visionAnnotateFilesURL, "",
		"purpose", "ocr",
		"mime", mimeType,
		"pages", pages,
	)
	resp, err := p.client.BatchAnnotateFiles(ctx, req)
	if err != nil {
		return fileAnnotateResult{}, visionError(err)
	}
	if len(resp.GetResponses()) == 0 {
		return fileAnnotateResult{}, fmt.Errorf("google vision returned no file responses")
	}

	fileResp := resp.GetResponses()[0]
	if fileResp.GetError() != nil {
		return fileAnnotateResult{}, fmt.Errorf("google vision: %s", fileResp.GetError().GetMessage())
	}

	pageTexts := make([]string, 0, len(fileResp.GetResponses()))
	for i, pageResp := range fileResp.GetResponses() {
		if pageResp.GetError() != nil {
			return fileAnnotateResult{}, fmt.Errorf("google vision page %d: %s", visionPageNumber(pages, i), pageResp.GetError().GetMessage())
		}
		if text := strings.TrimSpace(pageResp.GetFullTextAnnotation().GetText()); text != "" {
			pageTexts = append(pageTexts, text)
		}
	}

	p.logger.Info("google vision BatchAnnotateFiles",
		"mime", mimeType,
		"pages", pages,
		"extracted", len(pageTexts),
		"total_pages", fileResp.GetTotalPages(),
		"duration", time.Since(start).Round(time.Millisecond),
	)
	return fileAnnotateResult{
		pageTexts:  pageTexts,
		totalPages: int(fileResp.GetTotalPages()),
	}, nil
}

func visionPageNumber(pages []int32, index int) int32 {
	if index < len(pages) {
		return pages[index]
	}
	return int32(index + 1)
}

func (p *GoogleVisionProvider) extractImageText(ctx context.Context, content []byte, mimeType string) (string, error) {
	start := time.Now()
	req := &visionpb.BatchAnnotateImagesRequest{
		Requests: []*visionpb.AnnotateImageRequest{
			{
				Image:    &visionpb.Image{Content: content},
				Features: visionDocumentTextFeatures(),
			},
		},
	}

	aiprovider.LogRequest(p.logger, aiprovider.SDKGoogleVision, "POST", visionAnnotateImagesURL, "",
		"purpose", "ocr",
		"mime", mimeType,
	)
	resp, err := p.client.BatchAnnotateImages(ctx, req)
	if err != nil {
		return "", visionError(err)
	}
	if len(resp.GetResponses()) == 0 {
		return "", fmt.Errorf("google vision returned no responses")
	}

	imageResp := resp.GetResponses()[0]
	if imageResp.GetError() != nil {
		return "", fmt.Errorf("google vision: %s", imageResp.GetError().GetMessage())
	}

	text := imageResp.GetFullTextAnnotation().GetText()
	if text == "" {
		return "", fmt.Errorf("google vision returned empty text for mime type %s", mimeType)
	}

	p.logger.Info("google vision BatchAnnotateImages",
		"mime", mimeType,
		"chars", len(text),
		"duration", time.Since(start).Round(time.Millisecond),
	)
	return text, nil
}

func visionError(err error) error {
	return fmt.Errorf("google vision request: %w", err)
}
