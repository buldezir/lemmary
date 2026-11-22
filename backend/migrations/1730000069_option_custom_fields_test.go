package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// An admin adds, renames and deletes an option field of their own; the
// predefined ones stay locked, a user may not add one, and down removes the
// admin's option fields with the permission.
func TestOptionCustomFieldsAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)
	for range 2 {
		if err := allowOptionCustomFields(app); err != nil {
			t.Fatalf("allow on a migrated app: %v", err)
		}
	}
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		t.Fatal(err)
	}
	project, err := saveCustomField(app, "Project", models.CustomFieldOption)
	if err != nil {
		t.Fatalf("save option field: %v", err)
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
		name   string
		auth   *core.Record
		record string
		body   map[string]any
		rule   *string
		want   bool
	}{
		{"admin renames theirs", admin, project.Id, map[string]any{"name": "Case"}, coll.UpdateRule, true},
		{"admin retypes theirs", admin, project.Id, map[string]any{"type": models.CustomFieldText}, coll.UpdateRule, false},
		{"admin deletes theirs", admin, project.Id, nil, coll.DeleteRule, true},
		{"admin renames correspondent", admin, models.CorrespondentFieldID, map[string]any{"name": "Sender"}, coll.UpdateRule, false},
		{"admin deletes document type", admin, models.DocumentTypeFieldID, nil, coll.DeleteRule, false},
		{"user renames theirs", user, project.Id, map[string]any{"name": "Case"}, coll.UpdateRule, false},
	} {
		info := &core.RequestInfo{Auth: tc.auth, Body: tc.body}
		if got := canAccess(t, app, coll.Name, tc.record, info, tc.rule); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v", tc.name, got, tc.want)
		}
	}

	for range 2 {
		if err := disallowOptionCustomFields(app); err != nil {
			t.Fatalf("disallow: %v", err)
		}
	}
	if _, err := app.FindRecordById(coll, project.Id); err == nil {
		t.Fatal("down kept the admin's option field")
	}
	for _, field := range models.PredefinedCustomFields {
		if _, err := app.FindRecordById(coll, field.ID); err != nil {
			t.Fatalf("down removed %s: %v", field.ID, err)
		}
	}
}
