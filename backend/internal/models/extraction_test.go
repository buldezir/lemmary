package models_test

import (
	"testing"

	"lemmary/backend/internal/models"
)

func TestExtractedMetadataPopulated(t *testing.T) {
	if (&models.ExtractedMetadata{}).Populated() {
		t.Fatal("expected empty metadata not to be populated")
	}
	if (*models.ExtractedMetadata)(nil).Populated() {
		t.Fatal("expected nil metadata not to be populated")
	}
	if !(&models.ExtractedMetadata{Title: "Invoice"}).Populated() {
		t.Fatal("expected metadata with title to be populated")
	}
}

func TestParseExtractedMetadataValid(t *testing.T) {
	raw := `{
		"title": "Invoice 001",
		"purpose": "Office supplies",
		"document_date": "2024-03-15",
		"document_type": "invoice",
		"tags": ["invoice", "office"],
		"people_or_organizations": ["Acme Ltd."],
		"summary": "An invoice for office supplies.",
		"confidence": 0.92
	}`

	metadata, err := models.ParseExtractedMetadata(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if metadata.Title != "Invoice 001" {
		t.Fatalf("expected title Invoice 001, got %q", metadata.Title)
	}
	if metadata.DocumentDate != "2024-03-15" {
		t.Fatalf("expected date 2024-03-15, got %q", metadata.DocumentDate)
	}
}

func TestParseExtractedMetadataInvalidJSON(t *testing.T) {
	_, err := models.ParseExtractedMetadata("{not-json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseExtractedMetadataMissingTitle(t *testing.T) {
	raw := `{"title":"","confidence":0.5}`
	_, err := models.ParseExtractedMetadata(raw)
	if err == nil {
		t.Fatal("expected validation error for empty title")
	}
}

func TestParseExtractedMetadataStripsMarkdownFence(t *testing.T) {
	raw := "```json\n{\"title\":\"Test\",\"confidence\":0.8}\n```"
	metadata, err := models.ParseExtractedMetadata(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata.Title != "Test" {
		t.Fatalf("expected title Test, got %q", metadata.Title)
	}
}

func TestParseExtractedMetadataStripsReasoningTags(t *testing.T) {
	raw := "<think>\nThe user wants JSON metadata.\n</think>\n{\"title\":\"Invoice 001\",\"confidence\":0.9}"
	metadata, err := models.ParseExtractedMetadata(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata.Title != "Invoice 001" {
		t.Fatalf("expected title Invoice 001, got %q", metadata.Title)
	}
}

func TestParseExtractedMetadataTranslatedFields(t *testing.T) {
	raw := `{
		"title": "Rechnung 001",
		"title_translated": "Invoice 001",
		"summary": "Eine Rechnung.",
		"summary_translated": "An invoice.",
		"tags": ["Rechnung"],
		"tags_translated": ["Invoice"],
		"confidence": 0.9
	}`

	metadata, err := models.ParseExtractedMetadata(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata.TitleTranslated != "Invoice 001" {
		t.Fatalf("expected translated title, got %q", metadata.TitleTranslated)
	}
	if len(metadata.TagsTranslated) != 1 || metadata.TagsTranslated[0] != "Invoice" {
		t.Fatalf("expected translated tags, got %v", metadata.TagsTranslated)
	}
}

func TestParseExtractedMetadataCoercesPartialDate(t *testing.T) {
	// Issue #28: a model that answers a bare year must not fail the extraction.
	raw := `{"title":"Invoice 001","document_date":"2026","confidence":0.9}`

	metadata, notes, err := models.ParseExtractedMetadataWithNotes(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata.DocumentDate != "2026-01-01" {
		t.Fatalf("expected date 2026-01-01, got %q", metadata.DocumentDate)
	}
	if len(notes) != 1 {
		t.Fatalf("expected one repair note, got %v", notes)
	}
}

func TestParseExtractedMetadataDropsUnusableDate(t *testing.T) {
	raw := `{"title":"Invoice 001","document_date":"sometime last spring","confidence":0.9}`

	metadata, notes, err := models.ParseExtractedMetadataWithNotes(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata.Title != "Invoice 001" {
		t.Fatalf("expected the rest of the extraction to survive, got title %q", metadata.Title)
	}
	if metadata.DocumentDate != "" {
		t.Fatalf("expected the unusable date to be dropped, got %q", metadata.DocumentDate)
	}
	if len(notes) != 1 {
		t.Fatalf("expected one repair note, got %v", notes)
	}
}

func TestParseExtractedMetadataValidDateReportsNoNotes(t *testing.T) {
	raw := `{"title":"Invoice 001","document_date":"2024-03-15","confidence":0.9}`

	metadata, notes, err := models.ParseExtractedMetadataWithNotes(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata.DocumentDate != "2024-03-15" {
		t.Fatalf("expected date to pass through, got %q", metadata.DocumentDate)
	}
	if len(notes) != 0 {
		t.Fatalf("expected no repair notes, got %v", notes)
	}
}
