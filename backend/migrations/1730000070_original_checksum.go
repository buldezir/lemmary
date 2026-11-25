package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// original_checksum keeps the checksum a document's file had before unlock
// replaced it with the decrypted copy, so the locked file -- still in the
// owner's mail or drop folder -- is recognized as the same document when it
// arrives again. Not unique: checksum already is, and this only answers
// lookups.
func init() {
	m.Register(addOriginalChecksumField, func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return nil
		}
		documents.RemoveIndex("idx_documents_user_original_checksum")
		documents.Fields.RemoveByName("original_checksum")
		return app.Save(documents)
	})
}

func addOriginalChecksumField(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	if documents.Fields.GetByName("original_checksum") == nil {
		documents.Fields.Add(&core.TextField{Name: "original_checksum", Max: 64})
	}
	documents.AddIndex("idx_documents_user_original_checksum", false, "user, original_checksum", "original_checksum != ''")
	return app.Save(documents)
}
