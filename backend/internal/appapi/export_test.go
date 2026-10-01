package appapi

import (
	"testing"

	"lemmary/backend/internal/models"
)

// The archive carries names: the importing instance has its own ids.
func TestExportCustomFieldsKeysValuesByName(t *testing.T) {
	t.Parallel()
	values := models.FieldValues{
		"finvoice":                  {Type: models.CustomFieldText, Text: "R-1"},
		"famount":                   {Type: models.CustomFieldNumber, Number: 12.5},
		"fdeleted":                  {Type: models.CustomFieldText, Text: "orphan"},
		models.CorrespondentFieldID: {Type: models.CustomFieldOption, Option: "o1", OptionName: "Acme"},
	}

	names := map[string]string{"finvoice": "Invoice number", "famount": "Amount"}
	got := exportCustomFields(values, names)
	if len(got) != 2 || got["Invoice number"] != "R-1" || got["Amount"] != 12.5 {
		t.Fatalf("exported = %#v", got)
	}
}
