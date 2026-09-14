package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/models"
)

func TestDocumentsFileCapIsMaxFileBytesAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	file, ok := documents.Fields.GetByName("file").(*core.FileField)
	if !ok {
		t.Fatal("documents.file is not a file field")
	}
	if file.MaxSize != models.MaxFileBytes {
		t.Fatalf("documents.file MaxSize = %d, want %d", file.MaxSize, models.MaxFileBytes)
	}
	text, ok := documents.Fields.GetByName("ocr_text").(*core.TextField)
	if !ok {
		t.Fatal("documents.ocr_text is not a text field")
	}
	if text.Max != models.MaxOCRTextRunes {
		t.Fatalf("documents.ocr_text Max = %d, want %d", text.Max, models.MaxOCRTextRunes)
	}
}
