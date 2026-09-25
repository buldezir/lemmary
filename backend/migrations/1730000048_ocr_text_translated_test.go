package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func TestDocumentsOCRTextTranslatedIsHiddenAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	field, ok := documents.Fields.GetByName("ocr_text_translated").(*core.TextField)
	if !ok {
		t.Fatal("documents.ocr_text_translated is not a text field")
	}
	if !field.Hidden {
		t.Fatal("documents.ocr_text_translated must be hidden")
	}
	if field.Max != models.MaxOCRTextRunes {
		t.Fatalf("documents.ocr_text_translated Max = %d, want %d", field.Max, models.MaxOCRTextRunes)
	}
}
