package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
)

// Adds ocr_text_translated: the OCR text in the result language, filled on
// demand by the document page. Hidden so it stays out of list payloads and an
// owner cannot patch it; the translation route is its only reader and writer.
func init() {
	m.Register(func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		if documents.Fields.GetByName("ocr_text_translated") != nil {
			return nil
		}
		documents.Fields.Add(&core.TextField{
			Name:   "ocr_text_translated",
			Max:    models.MaxOCRTextRunes,
			Hidden: true,
		})
		return app.Save(documents)
	}, func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return nil
		}
		documents.Fields.RemoveByName("ocr_text_translated")
		return app.Save(documents)
	})
}
