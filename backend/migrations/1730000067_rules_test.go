package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
)

// A rule without a user is the admin's alone; a user's rule stays theirs, the
// admin's included.
func TestGlobalRulesAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)
	userRecord, err := app.FindRecordById("users", makeUser(t, app, "user@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	admin, err := app.FindRecordById("users", makeUser(t, app, "admin@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	admin.Set(pairedAdminField, true)
	if err := app.Save(admin); err != nil {
		t.Fatal(err)
	}
	coll, err := app.FindCollectionByNameOrId(rules.Collection)
	if err != nil {
		t.Fatal(err)
	}
	own := core.NewRecord(coll)
	own.Load(map[string]any{"user": userRecord.Id, "name": "Mine", "triggers": []string{rules.TriggerProcessed}})
	global := core.NewRecord(coll)
	global.Load(map[string]any{"name": "Everyone", "triggers": []string{rules.TriggerProcessed}})
	for _, record := range []*core.Record{own, global} {
		if err := app.Save(record); err != nil {
			t.Fatalf("save %s: %v", record.GetString("name"), err)
		}
	}

	for _, tc := range []struct {
		name   string
		auth   *core.Record
		record *core.Record
		rule   *string
		body   map[string]any
		want   bool
	}{
		{"admin reads a global rule", admin, global, coll.ViewRule, nil, true},
		{"admin edits a global rule", admin, global, coll.UpdateRule, map[string]any{"name": "All"}, true},
		{"admin hands it to a user", admin, global, coll.UpdateRule, map[string]any{"user": userRecord.Id}, false},
		{"user reads a global rule", userRecord, global, coll.ViewRule, nil, false},
		{"user deletes a global rule", userRecord, global, coll.DeleteRule, nil, false},
		{"user reads their own", userRecord, own, coll.ViewRule, nil, true},
		{"admin reads a user's rule", admin, own, coll.ViewRule, nil, false},
	} {
		info := &core.RequestInfo{Auth: tc.auth, Body: tc.body}
		if got := canAccess(t, app, coll.Name, tc.record.Id, info, tc.rule); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestRulesAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)
	for range 2 {
		if err := addRules(app); err != nil {
			t.Fatalf("add on a migrated app: %v", err)
		}
	}
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatal(err)
	}
	if jobs.Fields.GetByName(models.JobReturnStatus) == nil {
		t.Fatal("processing_jobs has no return_status")
	}

	owner := makeUser(t, app, "owner@example.com")
	ownerRecord, err := app.FindRecordById("users", owner)
	if err != nil {
		t.Fatal(err)
	}
	otherRecord, err := app.FindRecordById("users", makeUser(t, app, "other@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	coll, err := app.FindCollectionByNameOrId(rules.Collection)
	if err != nil {
		t.Fatal(err)
	}
	rule := core.NewRecord(coll)
	rule.Load(map[string]any{"user": owner, "name": "Invoices", "triggers": []string{rules.TriggerProcessed}})
	if err := app.Save(rule); err != nil {
		t.Fatalf("save rule: %v", err)
	}

	for _, tc := range []struct {
		name string
		auth *core.Record
		rule *string
		body map[string]any
		want bool
	}{
		{"owner reads", ownerRecord, coll.ViewRule, nil, true},
		{"someone else reads", otherRecord, coll.ViewRule, nil, false},
		{"owner renames", ownerRecord, coll.UpdateRule, map[string]any{"name": "Bills"}, true},
		{"owner gives it away", ownerRecord, coll.UpdateRule, map[string]any{"user": otherRecord.Id}, false},
		{"someone else deletes", otherRecord, coll.DeleteRule, nil, false},
	} {
		info := &core.RequestInfo{Auth: tc.auth, Body: tc.body}
		if got := canAccess(t, app, coll.Name, rule.Id, info, tc.rule); got != tc.want {
			t.Errorf("%s: allowed = %v, want %v", tc.name, got, tc.want)
		}
	}

	for range 2 {
		if err := dropRules(app); err != nil {
			t.Fatalf("drop: %v", err)
		}
	}
	if _, err := app.FindCollectionByNameOrId(rules.Collection); err == nil {
		t.Fatal("drop kept the rules collection")
	}
}
