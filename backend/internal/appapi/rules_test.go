package appapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
	"lemmary/backend/internal/rules"
)

// saveRuleRequest runs a rules create or update through the request hooks
// with body as what the client sent; record already holds it, as PocketBase
// loads the body into the record before the hooks run.
func saveRuleRequest(t *testing.T, app core.App, record *core.Record, body map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	e := &core.RecordRequestEvent{RequestEvent: &core.RequestEvent{App: app}, Record: record}
	e.Collection = record.Collection()
	e.Response = httptest.NewRecorder()
	e.Request = httptest.NewRequest(http.MethodPost, "/api/collections/rules/records", strings.NewReader(string(raw)))
	e.Request.Header.Set("Content-Type", "application/json")
	save := func(e *core.RecordRequestEvent) error { return e.App.Save(e.Record) }
	if record.IsNew() {
		return app.OnRecordCreateRequest().Trigger(e, save)
	}
	return app.OnRecordUpdateRequest().Trigger(e, save)
}

func TestValidateRule(t *testing.T) {
	app := bootQueueApp(t)
	app.OnRecordCreateRequest(rules.Collection).BindFunc(validateRule)
	app.OnRecordUpdateRequest(rules.Collection).BindFunc(validateRule)
	owner := makeQueueUser(t, app, "owner@example.com")
	tags, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatal(err)
	}
	tag := core.NewRecord(tags)
	tag.Set("user", owner)
	tag.Set("name", "to pay")
	if err := app.Save(tag); err != nil {
		t.Fatal(err)
	}
	coll, err := app.FindCollectionByNameOrId(rules.Collection)
	if err != nil {
		t.Fatal(err)
	}

	body := map[string]any{
		"user": owner, "name": "Invoices", "enabled": true, "triggers": []string{rules.TriggerProcessed},
		"conditions": []rules.Condition{{Subject: rules.SubjectTitle, Op: rules.OpContains, Value: "invoice"}},
		"actions":    []rules.Action{{Type: rules.ActionAddTag, Value: "not-a-tag"}},
	}
	record := core.NewRecord(coll)
	record.Load(body)
	err = saveRuleRequest(t, app, record, body)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("a rule adding an unknown tag was saved: %v", err)
	}

	body["actions"] = []rules.Action{{Type: rules.ActionAddTag, Value: tag.Id}}
	record = core.NewRecord(coll)
	record.Load(body)
	if err := saveRuleRequest(t, app, record, body); err != nil {
		t.Fatalf("a valid rule was refused: %v", err)
	}

	// Its tag is gone; switching it off must still work, editing it must not.
	if err := app.Delete(tag); err != nil {
		t.Fatal(err)
	}
	record.Set("enabled", false)
	if err := saveRuleRequest(t, app, record, map[string]any{"enabled": false}); err != nil {
		t.Fatalf("a toggle was refused over a stale tag: %v", err)
	}
	record.Set("name", "Renamed")
	if err := saveRuleRequest(t, app, record, map[string]any{"name": "Renamed", "actions": body["actions"]}); err == nil {
		t.Fatal("an edit of the actions kept a stale tag")
	}
}

// Only an update that changed tags or field values runs the edited rules, and
// the job takes the status the document had.
func TestRunRulesAfterEdit(t *testing.T) {
	app := bootQueueApp(t)
	app.OnRecordUpdateRequest("documents").BindFunc(runRulesAfterEdit)
	app.OnRecordUpdateRequest("documents").BindFunc(saveFieldsWithDocument)
	owner := makeQueueUser(t, app, "owner@example.com")
	doc := makeQueueDocument(t, app, owner, models.DocStatusNeedsReview, "text")
	coll, err := app.FindCollectionByNameOrId(rules.Collection)
	if err != nil {
		t.Fatal(err)
	}
	rule := core.NewRecord(coll)
	rule.Load(map[string]any{
		"user": owner, "name": "Edits", "enabled": true, "triggers": []string{rules.TriggerEdited},
		"actions": []rules.Action{{Type: rules.ActionRequireReview}},
	})
	if err := app.Save(rule); err != nil {
		t.Fatal(err)
	}
	jobs := func() []*core.Record {
		t.Helper()
		records, err := app.FindAllRecords("processing_jobs")
		if err != nil {
			t.Fatal(err)
		}
		return records
	}
	// Creating the document queued its own pipeline; this test is about edits.
	for _, job := range jobs() {
		if err := app.Delete(job); err != nil {
			t.Fatal(err)
		}
	}

	if err := updateDocument(t, app, doc.Id, "A new title", `{}`); err != nil {
		t.Fatal(err)
	}
	if n := len(jobs()); n != 0 {
		t.Fatalf("a title edit queued %d jobs", n)
	}

	if err := updateDocument(t, app, doc.Id, "", `{"`+models.DocumentTypeFieldID+`": "Invoice"}`); err != nil {
		t.Fatal(err)
	}
	queued := jobs()
	if len(queued) != 1 {
		t.Fatalf("jobs = %d, want one", len(queued))
	}
	var steps []string
	if err := queued[0].UnmarshalJSONField("steps", &steps); err != nil {
		t.Fatal(err)
	}
	if strings.Join(steps, ",") != strings.Join(models.EditedRulesSteps, ",") ||
		queued[0].GetString(models.JobReturnStatus) != models.DocStatusNeedsReview {
		t.Fatalf("job steps %v, return status %q", steps, queued[0].GetString(models.JobReturnStatus))
	}
}
