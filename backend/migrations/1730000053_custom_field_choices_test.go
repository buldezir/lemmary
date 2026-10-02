package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func saveChoice(app core.App, fieldID, name string) (*core.Record, error) {
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldChoicesCollection)
	if err != nil {
		return nil, err
	}
	record := core.NewRecord(coll)
	record.Set("field", fieldID)
	record.Set("name", name)
	return record, app.Save(record)
}

func TestCustomFieldChoicesAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)
	for range 2 {
		if err := addCustomFieldChoices(app); err != nil {
			t.Fatalf("add on a migrated app: %v", err)
		}
	}

	field, err := saveCustomField(app, "Status", models.CustomFieldChoice)
	if err != nil {
		t.Fatalf("save choice field: %v", err)
	}
	other, err := saveCustomField(app, "Note", models.CustomFieldText)
	if err != nil {
		t.Fatal(err)
	}
	paid, err := saveChoice(app, field.Id, "Paid")
	if err != nil {
		t.Fatalf("save choice: %v", err)
	}
	if _, err := saveChoice(app, field.Id, "PAID"); err == nil {
		t.Fatal("a choice differing only in case was accepted")
	}
	if _, err := saveChoice(app, other.Id, "Paid"); err != nil {
		t.Fatalf("the same name under another field: %v", err)
	}

	choices, err := app.FindCollectionByNameOrId(models.CustomFieldChoicesCollection)
	if err != nil {
		t.Fatal(err)
	}
	userID := makeUser(t, app, "user@example.com")
	user, err := app.FindRecordById("users", userID)
	if err != nil {
		t.Fatal(err)
	}
	adminID := makeUser(t, app, "admin@example.com")
	admin, err := app.FindRecordById("users", adminID)
	if err != nil {
		t.Fatal(err)
	}
	admin.Set(pairedAdminField, true)
	if err := app.Save(admin); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		auth *core.Record
		body map[string]any
		want bool
	}{
		{"admin renames", admin, map[string]any{"name": "Settled"}, true},
		{"admin moves", admin, map[string]any{"field": other.Id}, false},
		{"user renames", user, map[string]any{"name": "Settled"}, false},
	} {
		info := &core.RequestInfo{Auth: tc.auth, Body: tc.body}
		if got := canAccess(t, app, choices.Name, paid.Id, info, choices.UpdateRule); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v", tc.name, got, tc.want)
		}
	}
	if !canAccess(t, app, choices.Name, paid.Id, &core.RequestInfo{Auth: user}, choices.ViewRule) {
		t.Error("a signed-in user cannot read a choice")
	}

	for range 2 {
		if err := dropCustomFieldChoices(app); err != nil {
			t.Fatalf("drop: %v", err)
		}
	}
	if _, err := app.FindRecordById(models.CustomFieldsCollection, field.Id); err == nil {
		t.Fatal("drop kept a choice field")
	}
}
