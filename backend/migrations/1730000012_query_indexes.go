package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Every API read filters documents by owner (user = @request.auth.id) and the
// default listing sorts by created, yet the collection shipped with no index at
// all — every list was a full table scan. processing_jobs is looked up by
// document (including the cascade delete scan) and drained by (status, created).
func init() {
	m.Register(func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		documents.AddIndex("idx_documents_user_created", false, "user, created", "")
		if err := app.Save(documents); err != nil {
			return err
		}

		jobs, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return err
		}
		jobs.AddIndex("idx_processing_jobs_document", false, "document", "")
		jobs.AddIndex("idx_processing_jobs_status_created", false, "status, created", "")
		return app.Save(jobs)
	}, func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return nil
		}
		documents.RemoveIndex("idx_documents_user_created")
		if err := app.Save(documents); err != nil {
			return err
		}

		jobs, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return nil
		}
		jobs.RemoveIndex("idx_processing_jobs_document")
		jobs.RemoveIndex("idx_processing_jobs_status_created")
		return app.Save(jobs)
	})
}
