package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Adds the two columns the instance limits are measured in. They are written by
// the documents create hook, the one point every ingest path passes through, so
// the instance totals are a SUM over live rows with no counter to drift.
//
// Deliberately no backfill: filling them would mean running pdfinfo once per
// existing PDF, which makes boot time a function of library size. An upgraded
// install undercounts its existing library, which errs toward allowing more
// rather than locking an owner out of their own archive.
func init() {
	m.Register(func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		// Hidden so a regular account cannot write them: documents.UpdateRule lets
		// an owner patch their own row, and an update carrying only size_bytes = 0
		// would hand them an unlimited allowance.
		//
		// Hidden is only half of it: GrantSuperuserAccess is documented as
		// allowing writes to Hidden fields, so the documents update hook in
		// internal/limits restores both columns for everyone, which is what
		// actually closes it.
		if documents.Fields.GetByName("page_count") == nil {
			documents.Fields.Add(&core.NumberField{
				Name:    "page_count",
				Min:     new(0.0),
				OnlyInt: true,
				Hidden:  true,
			})
		}
		if documents.Fields.GetByName("size_bytes") == nil {
			documents.Fields.Add(&core.NumberField{
				Name:    "size_bytes",
				Min:     new(0.0),
				OnlyInt: true,
				Hidden:  true,
			})
		}
		// Covering index for the usage aggregate: the base table stores ocr_text
		// inline, up to models.MaxOCRTextRunes a row, so without it the sum scans
		// row bodies on every upload.
		documents.AddIndex("idx_documents_usage", false, "page_count, size_bytes", "")
		return app.Save(documents)
	}, func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return nil
		}
		if f := documents.Fields.GetByName("page_count"); f != nil {
			documents.Fields.RemoveById(f.GetId())
		}
		if f := documents.Fields.GetByName("size_bytes"); f != nil {
			documents.Fields.RemoveById(f.GetId())
		}
		documents.RemoveIndex("idx_documents_usage")
		return app.Save(documents)
	})
}
