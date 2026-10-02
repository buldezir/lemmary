package models_test

import (
	"slices"
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
	choice := models.CustomField{Type: models.CustomFieldChoice, Choices: []models.FieldChoice{{ID: "c1", Name: "Paid"}}}

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
		{"choice by name ignoring case", choice, " paid ", "c1", true},
		{"choice not listed", choice, "Overdue", nil, false},
		{"choice by id", choice, "c1", nil, false},
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

// One row per document and field: a save replaces, nil removes, an option has
// to be the document owner's and the field's, and a choice the field's.
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
	status := saveRecord(t, app, models.CustomFieldsCollection, map[string]any{"name": "Status", "type": models.CustomFieldChoice})
	paid := saveRecord(t, app, models.CustomFieldChoicesCollection, map[string]any{"field": status.Id, "name": "Paid"})
	priority := saveRecord(t, app, models.CustomFieldsCollection, map[string]any{"name": "Priority", "type": models.CustomFieldChoice})
	high := saveRecord(t, app, models.CustomFieldChoicesCollection, map[string]any{"field": priority.Id, "name": "High"})
	loaded, err := models.LoadCustomFields(app)
	if err != nil {
		t.Fatal(err)
	}
	statusField := loaded[slices.IndexFunc(loaded, func(f models.CustomField) bool { return f.ID == status.Id })]
	if len(statusField.Choices) != 1 || statusField.Choices[0] != (models.FieldChoice{ID: paid.Id, Name: "Paid"}) {
		t.Fatalf("Status choices = %#v", statusField.Choices)
	}

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
	if err := models.SaveFieldValue(app, doc, statusField, high.Id); err == nil {
		t.Error("another field's choice was accepted")
	}
	if err := models.SaveFieldValue(app, doc, statusField, paid.Id); err != nil {
		t.Fatalf("save choice: %v", err)
	}

	values, err := models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 3 || values[amount.Id].Value() != 99.0 || values.OptionName(models.CorrespondentFieldID) != "Acme" ||
		values[status.Id].Value() != "Paid" {
		t.Fatalf("values = %#v", values)
	}

	if err := models.SaveFieldValue(app, doc, field, nil); err != nil {
		t.Fatal(err)
	}
	values, err = models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil || len(values) != 2 {
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

// Choices keep their ids through any rename, a swap and a rotation included,
// which the unique name index would refuse taken one row at a time; values
// follow the id, and a choice left out goes with its values.
func TestSyncFieldChoices(t *testing.T) {
	app := testpb.Open(t)
	owner := newUser(t, app, "owner@example.com")
	file, err := filesystem.NewFileFromBytes([]byte("x"), "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc := saveRecord(t, app, "documents", map[string]any{"user": owner.Id, "file": file})
	status := saveRecord(t, app, models.CustomFieldsCollection, map[string]any{"name": "Status", "type": models.CustomFieldChoice})
	ids := map[string]string{}
	for _, name := range []string{"Paid", "Open", "Overdue"} {
		ids[name] = saveRecord(t, app, models.CustomFieldChoicesCollection, map[string]any{"field": status.Id, "name": name}).Id
	}
	field := models.CustomField{ID: status.Id, Name: "Status", Type: models.CustomFieldChoice, Choices: []models.FieldChoice{{ID: ids["Paid"]}}}
	if err := models.SaveFieldValue(app, doc, field, ids["Paid"]); err != nil {
		t.Fatal(err)
	}
	names := func() map[string]string {
		t.Helper()
		records, err := app.FindAllRecords(models.CustomFieldChoicesCollection)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, record := range records {
			out[record.Id] = record.GetString("name")
		}
		return out
	}

	sync := func(want ...models.FieldChoice) {
		t.Helper()
		if err := app.RunInTransaction(func(tx core.App) error {
			return models.SyncFieldChoices(tx, status.Id, want)
		}); err != nil {
			t.Fatalf("sync %v: %v", want, err)
		}
	}
	sync(models.FieldChoice{ID: ids["Paid"], Name: "Open"}, models.FieldChoice{ID: ids["Open"], Name: "Overdue"},
		models.FieldChoice{ID: ids["Overdue"], Name: "Paid"})
	if got := names(); got[ids["Paid"]] != "Open" || got[ids["Open"]] != "Overdue" || got[ids["Overdue"]] != "Paid" {
		t.Fatalf("after the rotation: %v", got)
	}
	values, err := models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil || values[status.Id].ChoiceName != "Open" {
		t.Fatalf("the value did not follow its choice: %#v, %v", values, err)
	}

	sync(models.FieldChoice{ID: ids["Open"], Name: "Overdue"}, models.FieldChoice{Name: "Disputed"})
	got := names()
	if len(got) != 2 || got[ids["Open"]] != "Overdue" {
		t.Fatalf("after removing two and adding one: %v", got)
	}
	values, err = models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil || len(values) != 0 {
		t.Fatalf("a removed choice kept its value: %#v, %v", values, err)
	}
}
