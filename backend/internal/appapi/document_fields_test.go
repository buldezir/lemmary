package appapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// updateDocument runs a documents update carrying fields through the hooks,
// the way PocketBase's record update handler does once the update rule has
// passed.
func updateDocument(t *testing.T, app core.App, docID, title, fields string) error {
	t.Helper()
	record, err := app.FindRecordById("documents", docID)
	if err != nil {
		t.Fatal(err)
	}
	if title != "" {
		record.Set("title", title)
	}
	e := &core.RecordRequestEvent{RequestEvent: &core.RequestEvent{App: app}, Record: record}
	e.Collection = record.Collection()
	e.Response = httptest.NewRecorder()
	e.Request = httptest.NewRequest(http.MethodPatch, "/api/collections/documents/records/"+docID,
		strings.NewReader(`{"fields": `+fields+`}`))
	e.Request.Header.Set("Content-Type", "application/json")
	return app.OnRecordUpdateRequest().Trigger(e, func(e *core.RecordRequestEvent) error {
		return e.App.Save(e.Record)
	})
}

// One update sets the predefined and the admin's fields with the document; a
// name becomes the owner's option, a value of the wrong type refuses the whole
// update without creating one, and a refused document keeps its values.
func TestSaveFieldsWithDocument(t *testing.T) {
	app := bootQueueApp(t)
	app.OnRecordUpdateRequest("documents").BindFunc(saveFieldsWithDocument)
	owner := makeQueueUser(t, app, "owner@example.com")
	doc := makeQueueDocument(t, app, owner, models.DocStatusCompleted, "text")
	fields, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		t.Fatal(err)
	}
	amount := core.NewRecord(fields)
	amount.Set("name", "Amount")
	amount.Set("type", models.CustomFieldNumber)
	if err := app.Save(amount); err != nil {
		t.Fatal(err)
	}
	status := core.NewRecord(fields)
	status.Set("name", "Status")
	status.Set("type", models.CustomFieldChoice)
	if err := app.Save(status); err != nil {
		t.Fatal(err)
	}
	choices, err := app.FindCollectionByNameOrId(models.CustomFieldChoicesCollection)
	if err != nil {
		t.Fatal(err)
	}
	paid := core.NewRecord(choices)
	paid.Set("field", status.Id)
	paid.Set("name", "Paid")
	if err := app.Save(paid); err != nil {
		t.Fatal(err)
	}
	values := func() models.FieldValues {
		t.Helper()
		values, err := models.LoadDocumentFieldValues(app, doc.Id)
		if err != nil {
			t.Fatal(err)
		}
		return values
	}

	if err := updateDocument(t, app, doc.Id, "Paid",
		`{"`+models.CorrespondentFieldID+`": "Acme", "`+amount.Id+`": "12.50", "`+status.Id+`": "paid"}`); err != nil {
		t.Fatal(err)
	}
	if got := values(); got.OptionName(models.CorrespondentFieldID) != "Acme" || got[amount.Id].Number != 12.5 ||
		got[status.Id].Choice != paid.Id {
		t.Fatalf("values = %#v", got)
	}

	for name, update := range map[string]struct{ title, fields string }{
		"a word for a number": {"", `{"` + amount.Id + `": "twelve", "` + models.DocumentTypeFieldID + `": "Invoice"}`},
		"an unknown field":    {"", `{"fnosuchfield00": "x"}`},
		"an unlisted choice":  {"", `{"` + status.Id + `": "Overdue"}`},
		"not an object":       {"", `"x"`},
		"a refused document":  {strings.Repeat("x", 501), `{"` + amount.Id + `": 1}`},
	} {
		if err := updateDocument(t, app, doc.Id, update.title, update.fields); err == nil {
			t.Errorf("%s: the update was accepted", name)
		}
	}
	if got := values(); got[amount.Id].Number != 12.5 || got.OptionID(models.DocumentTypeFieldID) != "" ||
		got[status.Id].ChoiceName != "Paid" {
		t.Fatalf("a refused update changed the values: %#v", got)
	}
	if n, _ := app.CountRecords(models.CustomFieldOptionsCollection); n != 1 {
		t.Fatalf("options = %d, want only Acme", n)
	}

	if err := updateDocument(t, app, doc.Id, "", `{"`+amount.Id+`": ""}`); err != nil {
		t.Fatal(err)
	}
	if _, ok := values()[amount.Id]; ok {
		t.Fatal("an empty value did not clear the field")
	}
}
