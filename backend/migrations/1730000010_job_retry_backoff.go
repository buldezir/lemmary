package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// next_attempt_at gates when a pending job becomes runnable again, so a failing
// step backs off instead of being retried immediately by the drain loop.
// Existing rows keep an empty value, which the worker treats as "due now".
func init() {
	m.Register(func(app core.App) error {
		jobs, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return err
		}
		if jobs.Fields.GetByName("next_attempt_at") == nil {
			jobs.Fields.Add(&core.DateField{Name: "next_attempt_at"})
		}
		jobs.AddIndex("idx_processing_jobs_status_next_attempt", false, "status, next_attempt_at", "")
		return app.Save(jobs)
	}, func(app core.App) error {
		jobs, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return nil
		}
		jobs.RemoveIndex("idx_processing_jobs_status_next_attempt")
		if f := jobs.Fields.GetByName("next_attempt_at"); f != nil {
			jobs.Fields.RemoveById(f.GetId())
		}
		return app.Save(jobs)
	})
}
