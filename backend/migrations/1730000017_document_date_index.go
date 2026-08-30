package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Covering index for the documents timeline, which counts an owner's documents
// grouped by the month in document_date.
//
// Same reasoning as idx_documents_usage: without an index the GROUP BY is a scan
// of the base table, and that table stores ocr_text inline -- up to
// models.MaxOCRTextRunes a row. With (user, document_date) the aggregate reads
// the index alone and never touches a row body. The column order matches the
// query: equality on user first, then the grouped column, which also leaves the
// rows already sorted by month within an owner.
//
// It also covers the From/To date filters on the documents list, which were
// falling back to idx_documents_user_created and filtering afterwards.
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
