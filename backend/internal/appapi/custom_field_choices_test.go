package appapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

// saveField runs a custom_fields create (empty id) or update carrying body
// through the hooks, the way PocketBase's record handler does once the rule
// has passed and the body is loaded into the record.
func saveField(t *testing.T, app core.App, id string, body map[string]any, raw string) (*core.Record, error) {
	t.Helper()
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(coll)
	method, trigger := http.MethodPost, app.OnRecordCreateRequest()
	if id != "" {
		if record, err = app.FindRecordById(coll, id); err != nil {
			t.Fatal(err)
		}
		method, trigger = http.MethodPatch, app.OnRecordUpdateRequest()
	}
	for key, value := range body {
		record.Set(key, value)
	}
	e := &core.RecordRequestEvent{RequestEvent: &core.RequestEvent{App: app}, Record: record}
	e.Collection = coll
	e.Response = httptest.NewRecorder()
	e.Request = httptest.NewRequest(method, "/api/collections/custom_fields/records", strings.NewReader(raw))
	e.Request.Header.Set("Content-Type", "application/json")
	return record, trigger.Trigger(e, func(e *core.RecordRequestEvent) error {
		return e.App.Save(e.Record)
	})
}

// A field and its choices are one save: a swap of two names goes through, and
// a save that fails partway leaves neither the field nor its earlier deletes.
func TestSaveChoicesWithField(t *testing.T) {
	app := bootQueueApp(t)
	app.OnRecordCreateRequest(models.CustomFieldsCollection).BindFunc(saveChoicesWithField)
	app.OnRecordUpdateRequest(models.CustomFieldsCollection).BindFunc(saveChoicesWithField)
	choices := func(fieldID string) map[string]string {
		t.Helper()
		fields, err := models.LoadCustomFields(app)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, field := range fields {
			if field.ID == fieldID {
				for _, choice := range field.Choices {
					out[choice.Name] = choice.ID
				}
			}
		}
		return out
	}
	choiceBody := map[string]any{"name": "Status", "type": models.CustomFieldChoice}

	long := strings.Repeat("x", models.MaxCustomFieldValueRunes+1)
	if _, err := saveField(t, app, "", choiceBody, `{"choices": [{"name": "Paid"}, {"name": "`+long+`"}]}`); err == nil {
		t.Fatal("a choice over the length limit was saved")
	}
	if n, _ := app.CountRecords(models.CustomFieldsCollection); n != int64(len(models.PredefinedCustomFields)) {
		t.Fatalf("a refused choice left its field behind: %d fields", n)
	}

	field, err := saveField(t, app, "", choiceBody, `{"choices": [{"name": " Paid "}, {"name": "Open"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	saved := choices(field.Id)
	if len(saved) != 2 || saved["Paid"] == "" || saved["Open"] == "" {
		t.Fatalf("choices = %v", saved)
	}

	swap := `{"choices": [{"id": "` + saved["Paid"] + `", "name": "Open"}, {"id": "` + saved["Open"] + `", "name": "Paid"}]}`
	if _, err := saveField(t, app, field.Id, nil, swap); err != nil {
		t.Fatalf("swap: %v", err)
	}
	if got := choices(field.Id); got["Open"] != saved["Paid"] || got["Paid"] != saved["Open"] {
		t.Fatalf("after the swap: %v", got)
	}

	failing := `{"choices": [{"id": "` + saved["Paid"] + `", "name": "` + long + `"}]}`
	if _, err := saveField(t, app, field.Id, nil, failing); err == nil {
		t.Fatal("a choice over the length limit was saved")
	}
	if got := choices(field.Id); len(got) != 2 {
		t.Fatalf("a failed save kept its delete: %v", got)
	}

	textField, err := saveField(t, app, "", map[string]any{"name": "Note", "type": models.CustomFieldText}, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for name, try := range map[string]struct{ id, raw string }{
		"no choices":         {field.Id, `{"choices": []}`},
		"a blank name":       {field.Id, `{"choices": [{"name": "  "}]}`},
		"a repeated name":    {field.Id, `{"choices": [{"name": "Paid"}, {"name": "PAID"}]}`},
		"another field's id": {field.Id, `{"choices": [{"id": "nosuchchoice00", "name": "Paid"}]}`},
		"a text field":       {textField.Id, `{"choices": [{"name": "Paid"}]}`},
		"not a list":         {field.Id, `{"choices": "Paid"}`},
	} {
		if _, err := saveField(t, app, try.id, nil, try.raw); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if got := choices(field.Id); len(got) != 2 {
		t.Fatalf("a refused save changed the choices: %v", got)
	}
}
