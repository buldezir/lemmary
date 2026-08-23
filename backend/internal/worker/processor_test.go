package worker

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/models"
)

func newTestDocumentRecord() *core.Record {
	documents := core.NewBaseCollection("documents")
	documents.Fields.Add(
		&core.TextField{Name: "title"},
		&core.TextField{Name: "title_original"},
		&core.TextField{Name: "summary"},
		&core.TextField{Name: "summary_original"},
		&core.TextField{Name: "purpose"},
		&core.TextField{Name: "purpose_original"},
	)
	return core.NewRecord(documents)
}

func TestApplyExtractedMetadataWithoutTranslation(t *testing.T) {
	document := newTestDocumentRecord()
	metadata := &models.ExtractedMetadata{
		Title:   "Rechnung 001",
		Summary: "Eine Rechnung für Büromaterial.",
		Purpose: "Archivierung",
	}

	applyExtractedMetadata(document, metadata, "")

	if got := document.GetString("title"); got != "Rechnung 001" {
		t.Fatalf("expected title Rechnung 001, got %q", got)
	}
	if got := document.GetString("title_original"); got != "" {
		t.Fatalf("expected empty title_original, got %q", got)
	}
}

func TestApplyExtractedMetadataWithTranslation(t *testing.T) {
	document := newTestDocumentRecord()
	metadata := &models.ExtractedMetadata{
		Title:             "Rechnung 001",
		TitleTranslated:   "Invoice 001",
		Summary:           "Eine Rechnung für Büromaterial.",
		SummaryTranslated: "An invoice for office supplies.",
		Purpose:           "Archivierung",
		PurposeTranslated: "Archiving",
	}

	applyExtractedMetadata(document, metadata, "en")

	if got := document.GetString("title"); got != "Invoice 001" {
		t.Fatalf("expected title Invoice 001, got %q", got)
	}
	if got := document.GetString("title_original"); got != "Rechnung 001" {
		t.Fatalf("expected title_original Rechnung 001, got %q", got)
	}
}

func TestDocumentTypeNamesWithoutTranslation(t *testing.T) {
	metadata := &models.ExtractedMetadata{DocumentType: "Rechnung"}
	display, original := documentTypeNames(metadata, "")
	if display != "Rechnung" || original != "Rechnung" {
		t.Fatalf("expected Rechnung/Rechnung, got %q/%q", display, original)
	}
}

func TestDocumentTypeNamesWithTranslation(t *testing.T) {
	metadata := &models.ExtractedMetadata{
		DocumentType:           "Rechnung",
		DocumentTypeTranslated: "Invoice",
	}
	display, original := documentTypeNames(metadata, "en")
	if display != "Invoice" || original != "Rechnung" {
		t.Fatalf("expected Invoice/Rechnung, got %q/%q", display, original)
	}
}

func TestMergeTagNames(t *testing.T) {
	got := mergeTagNames([]string{"Rechnung", "Büro"}, []string{"Invoice", "Office"})
	if len(got) != 4 {
		t.Fatalf("expected 4 unique tags, got %d: %v", len(got), got)
	}
}

func TestCorrespondentNamesFromExplicitField(t *testing.T) {
	metadata := &models.ExtractedMetadata{
		Correspondent:         "Acme GmbH",
		PeopleOrOrganizations: []string{"Other Ltd."},
	}
	display, original := correspondentNames(metadata, "")
	if display != "Acme GmbH" || original != "Acme GmbH" {
		t.Fatalf("expected Acme GmbH/Acme GmbH, got %q/%q", display, original)
	}
}

func TestCorrespondentNamesFallsBackToPeople(t *testing.T) {
	metadata := &models.ExtractedMetadata{
		PeopleOrOrganizations: []string{"Acme Ltd."},
	}
	display, original := correspondentNames(metadata, "")
	if display != "Acme Ltd." || original != "Acme Ltd." {
		t.Fatalf("expected Acme Ltd./Acme Ltd., got %q/%q", display, original)
	}
}

func TestCorrespondentNamesWithTranslation(t *testing.T) {
	metadata := &models.ExtractedMetadata{
		Correspondent:           "Acme GmbH",
		CorrespondentTranslated: "Acme Inc.",
	}
	display, original := correspondentNames(metadata, "en")
	if display != "Acme Inc." || original != "Acme GmbH" {
		t.Fatalf("expected Acme Inc./Acme GmbH, got %q/%q", display, original)
	}
}
