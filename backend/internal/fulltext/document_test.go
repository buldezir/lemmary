package fulltext

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// Custom values reach FieldAll; options also go to their own fields.
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
	all, _ := doc[FieldAll].(string)
	for _, want := range []string{"Invoice", "R-2026-001", "129.9", "Acme", "Overdue"} {
		if !strings.Contains(all, want) {
			t.Fatalf("FieldAll = %q, want it to contain %q", all, want)
		}
	}
	if doc[FieldCorrespondent] != "o1" || doc[FieldCorrespondentName] != "Acme" {
		t.Fatalf("correspondent fields = %v / %v", doc[FieldCorrespondent], doc[FieldCorrespondentName])
	}
}
