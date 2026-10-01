package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
)

// Lets one reprocess replace custom field values that extraction otherwise only
// fills in where they are empty: a flag on the job, set from the reprocess form.
func init() {
	m.Register(addJobOverwriteCustomFields, dropJobOverwriteCustomFields)
}

func addJobOverwriteCustomFields(app core.App) error {
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil {
		return err
	}
	if jobs.Fields.GetByName(models.JobOverwriteCustomFields) != nil {
		return nil
	}
	jobs.Fields.Add(&core.BoolField{Name: models.JobOverwriteCustomFields})
	return app.Save(jobs)
}

func dropJobOverwriteCustomFields(app core.App) error {
	jobs, err := app.FindCollectionByNameOrId("processing_jobs")
	if err != nil || jobs.Fields.GetByName(models.JobOverwriteCustomFields) == nil {
		return nil
	}
	jobs.Fields.RemoveByName(models.JobOverwriteCustomFields)
	return app.Save(jobs)
}
