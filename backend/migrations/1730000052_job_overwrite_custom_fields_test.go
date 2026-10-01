package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func TestJobOverwriteCustomFieldsIsABoolAndIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	for range 2 {
		if err := addJobOverwriteCustomFields(app); err != nil {
			t.Fatalf("add on a migrated app: %v", err)
		}
	}
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := jobs.Fields.GetByName(models.JobOverwriteCustomFields).(*core.BoolField); !ok {
		t.Fatalf("processing_jobs.%s is not a bool field", models.JobOverwriteCustomFields)
	}
	for range 2 {
		if err := dropJobOverwriteCustomFields(app); err != nil {
			t.Fatalf("drop: %v", err)
		}
	}
}
