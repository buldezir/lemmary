package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// error carries why a job failed, for failures that happen outside a step and
// so leave nothing in step_runs: an unparseable step list, a document that will
// not load, an unknown step name. Those used to be a log line only, which left
// the UI showing "Failed" with an empty step list and no way to explain it.
func init() {
	m.Register(func(app core.App) error {
		jobs, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return err
		}
		if jobs.Fields.GetByName("error") == nil {
			jobs.Fields.Add(&core.TextField{Name: "error", Max: 2000})
		}
		return app.Save(jobs)
	}, func(app core.App) error {
		jobs, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return nil
		}
		if f := jobs.Fields.GetByName("error"); f != nil {
			jobs.Fields.RemoveById(f.GetId())
		}
		return app.Save(jobs)
	})
}
