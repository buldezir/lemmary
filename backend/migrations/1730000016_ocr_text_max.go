package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
)

// Raises documents.ocr_text to the length a document's own file can produce.
//
// The column was declared at 500000 characters and nothing in the extraction
// pipeline knew that number: the OCR step sets the field and saves, so an
// extraction past the cap failed PocketBase's field validator inside app.Save
// rather than being shortened. With WORKER_MAX_RETRIES defaulting to 0 that
// failed the document for good -- the file stored, its text lost, and the
// reason buried in a validation error. A 20 MB .txt is roughly 21 million
// characters, so this was reachable by uploading a large text file, not only by
// trying to.
//
// See models.MaxOCRTextRunes for why the file cap is also a character cap, and
// for the two producers that needed a gate of their own instead.
//
// The value is set unconditionally rather than only when it is lower. Installs
// have already drifted from what the repo declares -- a dashboard edit writes a
// migration into the database that never reaches this directory -- so the point
// here is to state the length, not to negotiate with whatever is there.
func init() {
	m.Register(func(app core.App) error {
		return setOCRTextMax(app, models.MaxOCRTextRunes)
	}, func(app core.App) error {
		return setOCRTextMax(app, 500000)
	})
}

func setOCRTextMax(app core.App, max int) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	field, ok := documents.Fields.GetByName("ocr_text").(*core.TextField)
	if !ok || field == nil {
		return nil
	}
	field.Max = max
	return app.Save(documents)
}
