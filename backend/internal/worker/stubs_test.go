package worker

import (
	"context"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
	"lemmary/backend/internal/ocr"
)

type stubOCR struct{}

func (stubOCR) Name() string { return "stub-ocr" }
func (stubOCR) ExtractText(context.Context, string, string) (string, error) {
	return "", nil
}

type stubExtractor struct{}

func (stubExtractor) Name() string  { return "stub-ai" }
func (stubExtractor) Model() string { return "stub-model" }
func (stubExtractor) ExtractMetadata(context.Context, string, ai.ExtractionCatalog) (*models.ExtractedMetadata, error) {
	return nil, nil
}

type stubEmbedder struct{}

func (stubEmbedder) Name() string  { return "stub-embed" }
func (stubEmbedder) Model() string { return "stub-embed-model" }
func (stubEmbedder) Dims() int     { return 4 }
func (stubEmbedder) Embed(context.Context, []string) (ai.EmbedResult, error) {
	return ai.EmbedResult{}, nil
}

func snapshotWithProviders(o ocr.Provider, a ai.Extractor) config.Snapshot {
	return config.Snapshot{OCR: o, AI: a}
}
