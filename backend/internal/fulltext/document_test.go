package fulltext

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// Custom values reach FieldCustomFields; options go to their own fields.
func TestBuildIndexesFieldValues(t *testing.T) {
	documents := core.NewBaseCollection("documents")
	documents.Fields.Add(&core.TextField{Name: "title"})
	rec := core.NewRecord(documents)
	rec.Id = "doc1"
	rec.Set("title", "Invoice")
	names := &nameCache{values: map[string]models.FieldValues{"doc1": {
		"finvoice":                  {Type: models.CustomFieldText, Text: "R-2026-001"},
		"famount":                   {Type: models.CustomFieldNumber, Number: 129.9},
		"fstatus":                   {Type: models.CustomFieldChoice, Choice: "c1", ChoiceName: "Overdue"},
		models.CorrespondentFieldID: {Type: models.CustomFieldOption, Option: "o1", OptionName: "Acme"},
	}}}

	doc := buildWith(names, rec)
	if got := doc[FieldCustomFields]; got != "129.9 Overdue R-2026-001" {
		t.Fatalf("FieldCustomFields = %q", got)
	}
	if doc[FieldCorrespondent] != "o1" || doc[FieldCorrespondentName] != "Acme" {
		t.Fatalf("correspondent fields = %v / %v", doc[FieldCorrespondent], doc[FieldCorrespondentName])
	}
}

// A typed value is often the one thing about a document its OCR text does not
// say, so it alone finds the document.
func TestSearchFindsADocumentByItsCustomFieldValue(t *testing.T) {
	idx := testIndex(t)
	mustPut(t, idx, "doc", map[string]any{
		FieldUser:         "u1",
		FieldTitle:        "Invoice",
		FieldOCRText:      "nothing else",
		FieldCustomFields: "Overdue R-2026-001",
	})
	for _, text := range []string{"R-2026-001", "overdue"} {
		if ids := searchIDs(t, idx, Query{Text: text, UserID: "u1"}); !containsID(ids, "doc") {
			t.Fatalf("%q does not find the document: %v", text, ids)
		}
	}
}
