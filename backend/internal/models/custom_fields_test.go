package models_test

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	_ "lemmary/backend/migrations"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/testpb"
)

func TestCustomFieldCoerce(t *testing.T) {
	text := models.CustomField{Type: models.CustomFieldText}
	number := models.CustomField{Type: models.CustomFieldNumber}
	date := models.CustomField{Type: models.CustomFieldDate}
	option := models.CustomField{Type: models.CustomFieldOption}

	cases := []struct {
		name  string
		field models.CustomField
		in    any
		want  any
		ok    bool
	}{
		{"text trimmed", text, "  R-2026-001 ", "R-2026-001", true},
		{"text from number", text, 42.5, "42.5", true},
		{"text blank", text, "  ", "", false},
		{"text from object", text, map[string]any{}, nil, false},
		{"number", number, 129.9, 129.9, true},
		{"number from string", number, " 129.90 ", 129.9, true},
		{"number with currency", number, "129,90 EUR", nil, false},
		{"number NaN", number, "NaN", nil, false},
		{"date", date, "2026-10-31", "2026-10-31", true},
		{"date bare year", date, "2026", "2026-01-01", true},
		{"date unreadable", date, "end of month", "", false},
		{"date blank", date, "", "", false},
		{"date from number", date, 2026.0, nil, false},
		{"option id", option, " o123 ", "o123", true},
		{"option blank", option, "", "", false},
	}
	for _, tc := range cases {
		got, ok := tc.field.Coerce(tc.in)
		if ok != tc.ok || (ok && got != tc.want) {
			t.Errorf("%s: Coerce(%v) = %v, %v; want %v, %v", tc.name, tc.in, got, ok, tc.want, tc.ok)
		}
	}

	long, ok := text.Coerce(strings.Repeat("x", models.MaxCustomFieldValueRunes+10))
	if !ok || len([]rune(long.(string))) != models.MaxCustomFieldValueRunes {
		t.Fatalf("long text not capped at %d runes", models.MaxCustomFieldValueRunes)
	}
}

func saveRecord(t *testing.T, app core.App, collection string, values map[string]any) *core.Record {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(coll)
	for key, value := range values {
		record.Set(key, value)
	}
	if err := app.Save(record); err != nil {
		t.Fatalf("save %s: %v", collection, err)
	}
	return record
}

func newUser(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(coll)
	user.SetEmail(email)
	user.SetPassword("test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	return user
}

// One row per document and field: a save replaces, nil removes, and an option
// has to be the document owner's and the field's.
func TestSaveAndLoadFieldValues(t *testing.T) {
	app := testpb.Open(t)
	owner := newUser(t, app, "owner@example.com")
	other := newUser(t, app, "other@example.com")
	file, err := filesystem.NewFileFromBytes([]byte("x"), "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc := saveRecord(t, app, "documents", map[string]any{"user": owner.Id, "file": file})
	amount := saveRecord(t, app, models.CustomFieldsCollection, map[string]any{"name": "Amount", "type": models.CustomFieldNumber})
	field := models.CustomField{ID: amount.Id, Name: "Amount", Type: models.CustomFieldNumber}
	acme := saveRecord(t, app, models.CustomFieldOptionsCollection, map[string]any{
		"field": models.CorrespondentFieldID, "user": owner.Id, "name": "Acme",
	})
	foreign := saveRecord(t, app, models.CustomFieldOptionsCollection, map[string]any{
		"field": models.CorrespondentFieldID, "user": other.Id, "name": "Acme",
	})
	asType := saveRecord(t, app, models.CustomFieldOptionsCollection, map[string]any{
		"field": models.DocumentTypeFieldID, "user": owner.Id, "name": "Invoice",
	})

	for _, v := range []float64{12.5, 99} {
		if err := models.SaveFieldValue(app, doc, field, v); err != nil {
			t.Fatalf("save %v: %v", v, err)
		}
	}
	if err := models.SaveFieldValue(app, doc, models.CorrespondentField, acme.Id); err != nil {
		t.Fatalf("save own option: %v", err)
	}
	for name, id := range map[string]string{"another owner's": foreign.Id, "another field's": asType.Id} {
		if err := models.SaveFieldValue(app, doc, models.CorrespondentField, id); err == nil {
			t.Errorf("%s option was accepted", name)
		}
	}

	values, err := models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 2 || values[amount.Id].Value() != 99.0 || values.OptionName(models.CorrespondentFieldID) != "Acme" {
		t.Fatalf("values = %#v", values)
	}

	if err := models.SaveFieldValue(app, doc, field, nil); err != nil {
		t.Fatal(err)
	}
	values, err = models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil || len(values) != 1 {
		t.Fatalf("after removing Amount: %#v, %v", values, err)
	}
}

func TestParseExtractedMetadataCustomFields(t *testing.T) {
	metadata, err := models.ParseExtractedMetadata(`{"title":"Invoice","confidence":0.9,"custom_fields":{"Invoice number":"R-1","Amount":129.9}}`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if metadata.CustomFields["Invoice number"] != "R-1" || metadata.CustomFields["Amount"] != 129.9 {
		t.Fatalf("custom_fields = %#v", metadata.CustomFields)
	}

	metadata, err = models.ParseExtractedMetadata(`{"title":"Invoice","confidence":0.9,"custom_fields":["R-1"]}`)
	if err != nil {
		t.Fatalf("a custom_fields array must not fail the extraction: %v", err)
	}
	if metadata.CustomFields != nil {
		t.Fatalf("custom_fields = %#v, want nil", metadata.CustomFields)
	}
}
