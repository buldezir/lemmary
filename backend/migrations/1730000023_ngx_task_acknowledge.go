package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/ngxid"
)

// Adds the flag recording that a paperless client has dismissed a task.
//
// It exists only for the paperless-ngx API, which owns the column name and nothing else reads it. Lemmary's
// own UI shows a document's processing state on the document, so it has no
// notion of a task being dismissed; a paperless client does, and swift-
// paperless polls `GET /api/tasks/?acknowledged=false` and POSTs
// `/api/acknowledge_tasks/` to clear the ones it has shown. Without somewhere
// to record that, the acknowledge was a 404 and the same finished uploads came
// back on every poll forever.
const ngxAcknowledgedField = "ngx_acknowledged"

// Jobs also gain the client-facing id every addressable collection carries: an
// acknowledge names tasks by the id the task list reported, and that id was
// derived from a hash with no way back.
//
// Unique across the install rather than per account, because a job has no owner
// column -- it belongs to one only through its document. Reads stay scoped to
// the caller by joining that document.
func init() {
	m.Register(func(app core.App) error {
		const collection = "processing_jobs"

		jobs, err := app.FindCollectionByNameOrId(collection)
		if err != nil {
			return err
		}
		if jobs.Fields.GetByName(ngxAcknowledgedField) == nil {
			jobs.Fields.Add(&core.BoolField{Name: ngxAcknowledgedField})
		}
		if err := app.Save(jobs); err != nil {
			return err
		}

		if err := addNgxIDField(app, collection); err != nil {
			return err
		}
		if err := backfillNgxIDs(app, collection, ""); err != nil {
			return err
		}
		return indexNgxID(app, collection, "")
	}, func(app core.App) error {
		jobs, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return nil
		}
		jobs.RemoveIndex(ngxIDIndexName("processing_jobs"))
		for _, name := range []string{ngxid.Field, ngxAcknowledgedField} {
			if f := jobs.Fields.GetByName(name); f != nil {
				jobs.Fields.RemoveById(f.GetId())
			}
		}
		return app.Save(jobs)
	})
}
