package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
)

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
