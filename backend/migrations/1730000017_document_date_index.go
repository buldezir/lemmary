package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Covering index for the documents timeline, which counts an owner's documents
// grouped by the month in document_date.
//
// Same reasoning as idx_documents_usage: the documents table stores ocr_text
// inline, so without an index the GROUP BY reads row bodies. The column order
// matches the query, equality on user first, which also leaves the rows sorted
// by month within an owner. It also covers the From/To date filters on the
// documents list, which were falling back to idx_documents_user_created.
func init() {
	m.Register(func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		documents.AddIndex("idx_documents_user_document_date", false, "user, document_date", "")
		return app.Save(documents)
	}, func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return nil
		}
		documents.RemoveIndex("idx_documents_user_document_date")
		return app.Save(documents)
	})
}
