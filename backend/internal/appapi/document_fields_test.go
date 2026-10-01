package appapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func patchDocumentFields(t *testing.T, app core.App, authID, docID, body string) *httptest.ResponseRecorder {
	t.Helper()
	auth, err := app.FindRecordById("users", authID)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{App: app, Auth: auth}
	e.Response = rec
	e.Request = httptest.NewRequest(http.MethodPatch, "/api/app/documents/"+docID+"/fields", strings.NewReader(body))
	e.Request.Header.Set("Content-Type", "application/json")
	e.Request.SetPathValue("documentId", docID)
	if err := handlePatchDocumentFields(app)(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	return rec
}

// One request sets the predefined and the admin's fields; a name becomes the
// owner's option, a value of the wrong type refuses the whole request, and only
// the owner may write.
func TestPatchDocumentFields(t *testing.T) {
	app := bootQueueApp(t)
	owner := makeQueueUser(t, app, "owner@example.com")
	other := makeQueueUser(t, app, "other@example.com")
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

	rec := patchDocumentFields(t, app, owner, doc.Id,
		`{"`+models.CorrespondentFieldID+`": "Acme", "`+amount.Id+`": "12.50"}`)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body)
	}
	values, err := models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if values.OptionName(models.CorrespondentFieldID) != "Acme" || values[amount.Id].Number != 12.5 {
		t.Fatalf("values = %#v", values)
	}

	for name, body := range map[string]string{
		"a word for a number": `{"` + amount.Id + `": "twelve", "` + models.CorrespondentFieldID + `": null}`,
		"an unknown field":    `{"fnosuchfield00": "x"}`,
	} {
		if rec := patchDocumentFields(t, app, owner, doc.Id, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
	values, _ = models.LoadDocumentFieldValues(app, doc.Id)
	if values.OptionName(models.CorrespondentFieldID) != "Acme" {
		t.Fatal("a refused request changed the values")
	}

	if rec := patchDocumentFields(t, app, other, doc.Id, `{"`+amount.Id+`": 1}`); rec.Code != http.StatusNotFound {
		t.Fatalf("another user's write: status = %d, want 404", rec.Code)
	}

	if rec := patchDocumentFields(t, app, owner, doc.Id, `{"`+amount.Id+`": ""}`); rec.Code != http.StatusNoContent {
		t.Fatalf("clear: status = %d", rec.Code)
	}
	values, _ = models.LoadDocumentFieldValues(app, doc.Id)
	if _, ok := values[amount.Id]; ok {
		t.Fatal("an empty value did not clear the field")
	}
}
