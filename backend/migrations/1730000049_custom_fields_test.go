package migrations

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func saveCustomField(app core.App, name, fieldType string) (*core.Record, error) {
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(coll)
	record.Set("name", name)
	record.Set("type", fieldType)
	return record, app.Save(record)
}

func TestCustomFieldsCollectionAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	coll, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		t.Fatalf("custom_fields collection: %v", err)
	}
	if coll.CreateRule == nil || !strings.Contains(*coll.CreateRule, pairedAdminField) {
		t.Fatalf("create rule = %v, want admins only", coll.CreateRule)
	}
	for _, rule := range []*string{coll.CreateRule, coll.UpdateRule, coll.DeleteRule} {
		if rule == nil || !strings.Contains(*rule, "'option'") {
			t.Fatalf("rule %v leaves the predefined option fields writable", rule)
		}
	}
	for _, field := range models.PredefinedCustomFields {
		record, err := app.FindRecordById(coll, field.ID)
		if err != nil || record.GetString("type") != models.CustomFieldOption {
			t.Fatalf("predefined field %s: %v", field.ID, err)
		}
	}

	record, err := saveCustomField(app, "Invoice number", models.CustomFieldText)
	if err != nil {
		t.Fatalf("save field: %v", err)
	}
	if !strings.HasPrefix(record.Id, "f") {
		t.Fatalf("id %q lacks its letter prefix", record.Id)
	}
	if _, err := saveCustomField(app, "invoice NUMBER", models.CustomFieldNumber); err == nil {
		t.Fatal("a name differing only in case was accepted")
	}
	if _, err := saveCustomField(app, "Paid", "boolean"); err == nil {
		t.Fatal("an unknown type was accepted")
	}

	for _, name := range []string{models.CustomFieldOptionsCollection, models.CustomFieldValuesCollection} {
		if _, err := app.FindCollectionByNameOrId(name); err != nil {
			t.Fatalf("%s collection: %v", name, err)
		}
	}
	values, err := app.FindCollectionByNameOrId(models.CustomFieldValuesCollection)
	if err != nil {
		t.Fatal(err)
	}
	if values.CreateRule != nil || values.UpdateRule != nil || values.DeleteRule != nil {
		t.Fatal("custom_field_values must be written by the server only")
	}

	// An option is renamed in place, never moved to another field or owner.
	options, err := app.FindCollectionByNameOrId(models.CustomFieldOptionsCollection)
	if err != nil {
		t.Fatal(err)
	}
	ownerID := makeUser(t, app, "owner@example.com")
	other := makeUser(t, app, "other@example.com")
	owner, err := app.FindRecordById("users", ownerID)
	if err != nil {
		t.Fatal(err)
	}
	option := makeOption(t, app, models.CorrespondentFieldID, ownerID, "Acme")
	for body, want := range map[string]bool{
		"name":  true,
		"field": false,
		"user":  false,
	} {
		update := map[string]any{"name": "Acme GmbH", "field": models.DocumentTypeFieldID, "user": other}
		info := &core.RequestInfo{Auth: owner, Body: map[string]any{body: update[body]}}
		if got := canAccess(t, app, options.Name, option, info, options.UpdateRule); got != want {
			t.Errorf("changing %s: allowed = %v, want %v", body, got, want)
		}
	}
}

func TestCustomFieldsMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	for range 2 {
		if err := addCustomFields(app); err != nil {
			t.Fatalf("addCustomFields on an already-migrated app: %v", err)
		}
	}
}
