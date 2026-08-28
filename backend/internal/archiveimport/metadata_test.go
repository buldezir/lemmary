package archiveimport

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/models"
)

func testDocumentsCollection() *core.Collection {
	docs := core.NewBaseCollection("documents")
	docs.Fields.Add(
		&core.TextField{Name: "title"},
		&core.TextField{Name: "title_original"},
		&core.TextField{Name: "purpose"},
		&core.TextField{Name: "purpose_original"},
		&core.TextField{Name: "summary"},
		&core.TextField{Name: "summary_original"},
		&core.TextField{Name: "metadata_source"},
		&core.TextField{Name: "text_fingerprint"},
		&core.TextField{Name: "processing_status"},
		&core.TextField{Name: "document_date"},
		&core.NumberField{Name: "confidence"},
		&core.JSONField{Name: "people_or_organizations"},
	)
	return docs
}

func TestApplyMetadataRestoresFields(t *testing.T) {
	record := core.NewRecord(testDocumentsCollection())
	meta := map[string]any{
		"title":                   "Invoice 42",
		"title_original":          "Rechnung 42",
		"summary":                 "A summary",
		"metadata_source":         "ai",
		"text_fingerprint":        "deadbeefdeadbeef",
		"document_date":           "15.03.2026",
		"processing_status":       models.DocStatusCompleted,
		"confidence":              0.75,
		"people_or_organizations": []any{"Acme", "  ", "Jane"},
	}

	if err := applyMetadata(record, meta, newTaxonomyResolver(nil, "owner", &Result{})); err != nil {
		t.Fatalf("applyMetadata: %v", err)
	}

	if record.GetString("title") != "Invoice 42" || record.GetString("title_original") != "Rechnung 42" {
		t.Fatalf("titles=%q/%q", record.GetString("title"), record.GetString("title_original"))
	}
	if record.GetString("summary") != "A summary" || record.GetString("text_fingerprint") != "deadbeefdeadbeef" {
		t.Fatalf("record=%#v", record.PublicExport())
	}
	// A day-first date from another instance is normalized to the stored form.
	if record.GetString("document_date") != "2026-03-15" {
		t.Fatalf("document_date=%q", record.GetString("document_date"))
	}
	if record.GetString("processing_status") != models.DocStatusCompleted {
		t.Fatalf("status=%q", record.GetString("processing_status"))
	}
	if record.GetFloat("confidence") != 0.75 {
		t.Fatalf("confidence=%v", record.GetFloat("confidence"))
	}
	if people := models.PeopleOrOrganizations(record); len(people) != 2 || people[1] != "Jane" {
		t.Fatalf("people=%#v", people)
	}
}

// Fields the collection would reject are dropped, so one bad value in a sidecar
// cannot take down the document it belongs to.
func TestApplyMetadataDropsUnusableValues(t *testing.T) {
	record := core.NewRecord(testDocumentsCollection())
	meta := map[string]any{
		"title":             "  ",
		"document_date":     "not a date",
		"processing_status": models.DocStatusProcessing,
		"confidence":        4.2,
		"tags":              "not-an-array",
	}

	if err := applyMetadata(record, meta, newTaxonomyResolver(nil, "owner", &Result{})); err != nil {
		t.Fatalf("applyMetadata: %v", err)
	}

	if record.GetString("title") != "" || record.GetString("document_date") != "" {
		t.Fatalf("record=%#v", record.PublicExport())
	}
	// Nothing is mid-run in a fresh restore.
	if record.GetString("processing_status") != "" {
		t.Fatalf("status=%q", record.GetString("processing_status"))
	}
	// The collection bounds confidence to 0..1.
	if record.GetFloat("confidence") != 0 {
		t.Fatalf("confidence=%v", record.GetFloat("confidence"))
	}
}

func TestParseTimestamp(t *testing.T) {
	stored := types.NowDateTime().String()
	got, ok := parseTimestamp(stored)
	if !ok || got != stored {
		t.Fatalf("got %q ok=%v want %q", got, ok, stored)
	}
	for _, bad := range []string{"", "   ", "yesterday"} {
		if _, ok := parseTimestamp(bad); ok {
			t.Fatalf("parseTimestamp(%q) should not parse", bad)
		}
	}
}
