package duplicates

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func newDuplicateTestRecord(id, duplicateOf string) *core.Record {
	documents := core.NewBaseCollection("documents")
	documents.Fields.Add(
		&core.TextField{Name: "duplicate_of"},
		&core.TextField{Name: "processing_status"},
	)
	record := core.NewRecord(documents)
	record.Id = id
	record.Set("duplicate_of", duplicateOf)
	return record
}

// An already-linked document must report marked=false so a repeated scan does
// not count it again; a cleared checksum makes this path run on every scan.
func TestMarkAsDuplicateReportsNoOpForAlreadyLinked(t *testing.T) {
	t.Parallel()

	original := newDuplicateTestRecord("originalid00001", "")
	document := newDuplicateTestRecord("duplicateid0001", "originalid00001")

	// A nil app is safe here precisely because no save should be attempted.
	marked, err := MarkAsDuplicate(nil, document, original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if marked {
		t.Fatal("expected marked=false for an already-linked document")
	}
	if got := document.GetString("processing_status"); got != "" {
		t.Fatalf("expected status untouched, got %q", got)
	}
}

func TestMarkAsDuplicateRejectsMissingRecords(t *testing.T) {
	t.Parallel()

	if _, err := MarkAsDuplicate(nil, nil, newDuplicateTestRecord("originalid00001", "")); err == nil {
		t.Fatal("expected an error for a nil document")
	}
	if _, err := MarkAsDuplicate(nil, newDuplicateTestRecord("duplicateid0001", ""), nil); err == nil {
		t.Fatal("expected an error for a nil original")
	}
}

func TestDuplicateStatusConstant(t *testing.T) {
	t.Parallel()

	if models.DocStatusNeedsReview != "needs_review" {
		t.Fatalf("unexpected needs-review status %q", models.DocStatusNeedsReview)
	}
}
