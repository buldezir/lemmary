package worker

import (
	"context"
	"log/slog"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/models"
)

// Extraction fills only the empty fields: a value typed by hand survives a
// reprocess, an answer for no defined field is dropped, and one that does not
// fit its type is not stored.
func TestApplyMetadataFillsOnlyEmptyCustomFields(t *testing.T) {
	app := bootTagTestApp(t)
	owner := createTagUser(t, app, "owner@example.com")

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatal(err)
	}

	definitions, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for name, fieldType := range map[string]string{
		"Invoice number": models.CustomFieldText,
		"Amount":         models.CustomFieldNumber,
		"Due date":       models.CustomFieldDate,
		"IBAN":           models.CustomFieldNumber,
	} {
		definition := core.NewRecord(definitions)
		definition.Set("name", name)
		definition.Set("type", fieldType)
		if err := app.Save(definition); err != nil {
			t.Fatalf("save field %s: %v", name, err)
		}
		ids[name] = definition.Id
	}

	doc := core.NewRecord(documents)
	doc.Set("user", owner)
	doc.Set("title", "pending")
	file, err := filesystem.NewFileFromBytes([]byte("x"), "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	doc.Set("file", file)
	if err := app.Save(doc); err != nil {
		t.Fatalf("save document: %v", err)
	}
	typed := models.CustomField{ID: ids["Invoice number"], Name: "Invoice number", Type: models.CustomFieldText}
	if err := models.SaveFieldValue(app, doc, typed, "typed by hand"); err != nil {
		t.Fatal(err)
	}
	job := core.NewRecord(jobs)
	job.Set("document", doc.Id)
	job.Set("status", models.JobStatusRunning)
	job.Set("steps", []string{models.StepApplyMetadata})

	state := &StepState{
		App:      app,
		Job:      job,
		Document: doc,
		Logger:   slog.Default(),
		Metadata: &models.ExtractedMetadata{
			Title:      "Invoice",
			Confidence: 0.9,
			CustomFields: map[string]any{
				"Invoice number": "R-2026-001",
				"amount":         "129.90",
				"Due date":       "31.10.2026",
				"IBAN":           "DE89 3704",
				"Customer":       "unasked",
			},
		},
	}
	if err := (&ApplyMetadataStep{}).Run(context.Background(), state); err != nil {
		t.Fatalf("apply: %v", err)
	}

	values, err := models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for id, value := range values {
		got[id] = value.Value()
	}
	want := map[string]any{ids["Invoice number"]: "typed by hand", ids["Amount"]: 129.9, ids["Due date"]: "2026-10-31"}
	if len(got) != len(want) {
		t.Fatalf("custom_fields = %#v, want %#v", got, want)
	}
	for id, v := range want {
		if got[id] != v {
			t.Fatalf("custom_fields[%s] = %#v, want %#v (all: %#v)", id, got[id], v, got)
		}
	}

	// A job queued to overwrite replaces what extraction answered, and only
	// that: a field it found nothing for keeps its value.
	job.Set(models.JobOverwriteCustomFields, true)
	state.Metadata.CustomFields = map[string]any{"Invoice number": "R-2026-001"}
	if err := (&ApplyMetadataStep{}).Run(context.Background(), state); err != nil {
		t.Fatalf("apply with overwrite: %v", err)
	}
	values, err = models.LoadDocumentFieldValues(app, doc.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := values[ids["Invoice number"]].Value(); got != "R-2026-001" {
		t.Fatalf("overwritten invoice number = %#v", got)
	}
	if got := values[ids["Amount"]].Value(); got != 129.9 {
		t.Fatalf("an unanswered field lost its value: %#v", got)
	}
}
