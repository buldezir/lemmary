package worker

import (
	"context"

	"paperless-go/backend/internal/ai"
	"paperless-go/backend/internal/config"
	"paperless-go/backend/internal/models"
	"paperless-go/backend/internal/ocr"
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

func snapshotWithProviders(o ocr.Provider, a ai.Extractor) config.Snapshot {
	return config.Snapshot{OCR: o, AI: a}
}
