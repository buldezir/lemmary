package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Adds the two columns the instance limits are measured in.
//
// They are written by the documents create hook, which is the one point every
// ingest path passes through, so a document created from here on carries its own
// size and page count and the instance totals are a SUM over live rows -- no
// counter to drift, and deleting a document frees its allowance for free.
//
// Deliberately no backfill: rows that predate this migration keep 0 in both
// columns. Filling them would mean running pdfinfo once per existing PDF, which
// makes boot time a function of library size. The cost of leaving them is that an
// upgraded install undercounts its existing library, which errs toward allowing
// more rather than locking an owner out of their own archive.
func init() {
	m.Register(func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		// Hidden so a regular account cannot write them: documents.UpdateRule
		// lets an owner patch their own row, and an update carrying only
		// size_bytes = 0 would otherwise hand them an unlimited allowance.
		//
		// Hidden is only half of it, and on its own would be a false comfort.
		// PocketBase restores a hidden field from the stored value for a
		// non-superuser write, but GrantSuperuserAccess is documented as
		// allowing "changing all system record fields, including those marked
		// as Hidden". The documents update hook in internal/limits restores
		// both columns for everyone, which is what actually closes it.
		if documents.Fields.GetByName("page_count") == nil {
			documents.Fields.Add(&core.NumberField{
				Name:    "page_count",
				Min:     types.Pointer(0.0),
				OnlyInt: true,
				Hidden:  true,
			})
		}
		if documents.Fields.GetByName("size_bytes") == nil {
			documents.Fields.Add(&core.NumberField{
				Name:    "size_bytes",
				Min:     types.Pointer(0.0),
				OnlyInt: true,
				Hidden:  true,
			})
		}
		// Covering index for the usage aggregate. Without it, summing these two
		// columns is a scan of the base table -- which stores ocr_text inline,
		// up to 500 KB a row -- on every upload. With it the sum never touches
		// the row bodies.
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
