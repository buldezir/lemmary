package appapi

import (
	"errors"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
	"lemmary/backend/internal/worker"
)

// Merging "H.M" into "H&M" moves its documents and the owner's rules over and
// deletes it; another user's option, the other field and itself are refused.
func TestMergeOption(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
	option := func(user, field, name string) string {
		t.Helper()
		id, _, err := worker.EnsureOption(app, field, user, name)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	dotted := option(owner, models.CorrespondentFieldID, "H.M")
	amp := option(owner, models.CorrespondentFieldID, "H&M")
	if dotted == amp {
		t.Fatal(`"H&M" resolved to "H.M"`)
	}
	invoice := option(owner, models.DocumentTypeFieldID, "Invoice")
	theirs := option(other, models.CorrespondentFieldID, "H&M")

	docs := []*core.Record{
		makeQueueDocument(t, app, owner, models.DocStatusCompleted, "a"),
		makeQueueDocument(t, app, owner, models.DocStatusCompleted, "b"),
	}
	for _, doc := range docs {
		if err := models.SaveFieldValue(app, doc, models.CorrespondentField, dotted); err != nil {
			t.Fatal(err)
		}
	}
	coll, err := app.FindCollectionByNameOrId(rules.Collection)
	if err != nil {
		t.Fatal(err)
	}
	rule := core.NewRecord(coll)
	rule.Load(map[string]any{
		"user": owner, "name": "H&M", "enabled": true, "triggers": []string{rules.TriggerProcessed},
		"conditions": []rules.Condition{{Subject: models.CorrespondentFieldID, Op: rules.OpIs, Value: dotted}},
		"actions":    []rules.Action{{Type: rules.ActionSetField, Field: models.CorrespondentFieldID, Value: dotted}},
	})
	if err := app.Save(rule); err != nil {
		t.Fatal(err)
	}

	for name, ids := range map[string][2]string{
		"itself":         {dotted, dotted},
		"another field":  {dotted, invoice},
		"another user's": {dotted, theirs},
		"a missing one":  {"nosuchoption00", amp},
	} {
		if _, err := mergeOption(app, owner, ids[0], ids[1]); err == nil {
			t.Errorf("merging into %s was accepted", name)
		}
	}
	if _, err := mergeOption(app, owner, dotted, theirs); !errors.Is(err, errOptionNotFound) {
		t.Fatalf("another user's option: %v", err)
	}

	moved, err := mergeOption(app, owner, dotted, amp)
	if err != nil {
		t.Fatal(err)
	}
	if moved != len(docs) {
		t.Fatalf("moved = %d, want %d", moved, len(docs))
	}
	for _, doc := range docs {
		values, err := models.LoadDocumentFieldValues(app, doc.Id)
		if err != nil {
			t.Fatal(err)
		}
		if got := values.OptionName(models.CorrespondentFieldID); got != "H&M" {
			t.Fatalf("correspondent = %q, want H&M", got)
		}
	}
	if _, err := app.FindRecordById(models.CustomFieldOptionsCollection, dotted); err == nil {
		t.Fatal("the merged option is still there")
	}
	saved, err := app.FindRecordById(rules.Collection, rule.Id)
	if err != nil {
		t.Fatal(err)
	}
	got, err := rules.FromRecord(saved)
	if err != nil {
		t.Fatal(err)
	}
	if got.Conditions[0].Value != amp || got.Actions[0].Value != amp {
		t.Fatalf("rule still points at the merged option: %+v %+v", got.Conditions, got.Actions)
	}
}
