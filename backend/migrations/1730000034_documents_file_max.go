package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
)

// Raises documents.file from 20 MiB to models.MaxFileBytes, just under Mistral
// OCR's documented 50 MB, and ocr_text with it so a plain-text upload of that size
// still has a column to land in. Set unconditionally for the same reason as
// 1730000016: installs drift, and the point is to state the cap, not negotiate.
func init() {
	m.Register(func(app core.App) error {
		return setDocumentFileMax(app, models.MaxFileBytes, models.MaxOCRTextRunes)
	}, func(app core.App) error {
		return setDocumentFileMax(app, 20<<20, 20<<20)
	})
}

func setDocumentFileMax(app core.App, fileBytes int64, textRunes int) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	if field, ok := documents.Fields.GetByName("file").(*core.FileField); ok && field != nil {
		field.MaxSize = fileBytes
	}
	if err := app.Save(documents); err != nil {
		return err
	}
	return setOCRTextMax(app, textRunes)
}
