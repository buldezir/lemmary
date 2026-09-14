package embedstore

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"
)

// newDocumentRecord builds a documents record already "loaded from the
// database", so Original() answers with the values it was given. PostScan is
// what makes the current values the original ones.
func newDocumentRecord(t *testing.T, id, title, ocrText string) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection("documents")
	collection.Fields.Add(
		&core.TextField{Name: "title"},
		&core.TextField{Name: "summary"},
		&core.TextField{Name: "ocr_text"},
		&core.TextField{Name: "metadata_source"},
		&core.SelectField{Name: "processing_status", Values: []string{"needs_review", "completed"}},
		&core.JSONField{Name: "tags"},
		&core.JSONField{Name: "people_or_organizations"},
	)
	record := core.NewRecord(collection)
	record.Id = id
	record.Set("title", title)
	record.Set("ocr_text", ocrText)
	record.Set("tags", []string{"tag1"})
	record.Set("processing_status", "needs_review")
	if err := record.PostScan(); err != nil {
		t.Fatalf("PostScan: %v", err)
	}
	return record
}

// Only the OCR text is embedded, so only the OCR text can date a vector. This
// is the contract the whole feature rests on: an archive whose tags are being
// tidied must not be re-embedded for it.
func TestTouchesEmbeddedTextOnlyFollowsTheOCRText(t *testing.T) {
	t.Parallel()

	edited := newDocumentRecord(t, "doc1", "Policy", "Acme Plumbing\nTotal 42.00\n")
	edited.Set("ocr_text", "Acme Plumbing\nTotal 24.00\n")
	if !touchesEmbeddedText(edited) {
		t.Fatal("a corrected total is different text and has to date the vectors")
	}

	for name, edit := range map[string]func(*core.Record){
		"a new title":      func(r *core.Record) { r.Set("title", "Plumbing invoice") },
		"a new summary":    func(r *core.Record) { r.Set("summary", "Paid in February.") },
		"another tag":      func(r *core.Record) { r.Set("tags", []string{"tag1", "tag2"}) },
		"a status flip":    func(r *core.Record) { r.Set("processing_status", "completed") },
		"a metadata stamp": func(r *core.Record) { r.Set("metadata_source", "user") },
	} {
		t.Run(name, func(t *testing.T) {
			record := newDocumentRecord(t, "doc1", "Policy", "Acme Plumbing\nTotal 42.00\n")
			edit(record)
			if touchesEmbeddedText(record) {
				t.Fatalf("%s does not change a single vector and must not date one", name)
			}
		})
	}
}

// The detail form sends ocr_text on every save, including a save that only
// corrected a title. Re-writing the same text must not date the vectors: a
// re-embed of an unchanged 20-page document costs a provider call and buys
// nothing.
func TestTouchesEmbeddedTextIgnoresAnUnchangedOCRSave(t *testing.T) {
	t.Parallel()
	record := newDocumentRecord(t, "doc1", "Policy", "Acme Plumbing\nTotal 42.00\n")

	// What "Save corrections" writes when only the title was touched: the same
	// ocr_text back, plus the fields the save always sets.
	record.Set("title", "Plumbing invoice")
	record.Set("ocr_text", "Acme Plumbing\nTotal 42.00\n")
	record.Set("metadata_source", "user")
	record.Set("processing_status", "completed")
	if touchesEmbeddedText(record) {
		t.Fatal("re-writing the same ocr_text must not mark the document stale")
	}
}

// A record with no previous state is a record this cannot reason about, and
// guessing "unchanged" there would leave a document permanently unembedded.
func TestTouchesEmbeddedTextTreatsAnUnknownOriginalAsChanged(t *testing.T) {
	t.Parallel()

	if touchesEmbeddedText(nil) {
		t.Fatal("no record is not a change")
	}
	collection := core.NewBaseCollection("documents")
	collection.Fields.Add(&core.TextField{Name: "ocr_text"})
	created := core.NewRecord(collection)
	created.Set("ocr_text", "text")
	if !touchesEmbeddedText(created) {
		t.Fatal("a record with no previous state has to be treated as changed")
	}
}
