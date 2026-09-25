package appapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/models"
)

type fakeTranslator struct{ calls int }

func (f *fakeTranslator) Name() string  { return "fake" }
func (f *fakeTranslator) Model() string { return "fake-model" }
func (f *fakeTranslator) ExtractMetadata(context.Context, string, ai.ExtractionCatalog) (*models.ExtractedMetadata, error) {
	return nil, nil
}
func (f *fakeTranslator) Translate(_ context.Context, text string) (string, error) {
	f.calls++
	return "DE: " + text, nil
}

func callTranslation(t *testing.T, app core.App, tr *fakeTranslator, userID, documentID, query string) (int, string) {
	t.Helper()
	user, err := app.FindRecordById("users", userID)
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	e := &core.RequestEvent{}
	e.Response = rec
	e.Request = httptest.NewRequest(http.MethodPost, "/api/app/documents/"+documentID+"/translation"+query, nil)
	e.Request.SetPathValue("documentId", documentID)
	e.Auth = user
	snapshot := func() config.Snapshot {
		return config.Snapshot{Cfg: config.Config{ProcessingResultLanguage: "de"}, AI: tr}
	}
	if err := handleDocumentTranslation(app, snapshot)(e); err != nil {
		t.Fatalf("handler: %v", err)
	}
	var body struct{ Text string }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Text
}

func TestDocumentTranslationStoresAndReusesTheTranslation(t *testing.T) {
	app := bootQueueApp(t)
	app.OnRecordUpdate("documents").BindFunc(clearStaleTranslation)
	owner := makeQueueUser(t, app, "owner@example.com")
	stranger := makeQueueUser(t, app, "stranger@example.com")
	doc := makeQueueDocument(t, app, owner, "completed", "Rechnung")
	tr := &fakeTranslator{}

	if code, _ := callTranslation(t, app, tr, stranger, doc.Id, ""); code != http.StatusForbidden {
		t.Fatalf("stranger status = %d, want 403", code)
	}
	if code, text := callTranslation(t, app, tr, owner, doc.Id, ""); code != http.StatusOK || text != "DE: Rechnung" {
		t.Fatalf("first call = %d %q", code, text)
	}
	if _, text := callTranslation(t, app, tr, owner, doc.Id, ""); text != "DE: Rechnung" || tr.calls != 1 {
		t.Fatalf("second call = %q after %d model calls, want the stored text after 1", text, tr.calls)
	}
	if _, _ = callTranslation(t, app, tr, owner, doc.Id, "?force=1"); tr.calls != 2 {
		t.Fatalf("force made %d model calls, want 2", tr.calls)
	}

	fresh, err := app.FindRecordById("documents", doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	fresh.Set("ocr_text", "Mahnung")
	if err := app.Save(fresh); err != nil {
		t.Fatal(err)
	}
	if fresh, _ = app.FindRecordById("documents", doc.Id); fresh.GetString("ocr_text_translated") != "" {
		t.Fatalf("editing ocr_text should clear the translation, got %q", fresh.GetString("ocr_text_translated"))
	}
	if _, text := callTranslation(t, app, tr, owner, doc.Id, ""); text != "DE: Mahnung" {
		t.Fatalf("after edit = %q", text)
	}
}
