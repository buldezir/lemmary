package migrations

import "testing"

func TestOriginalChecksumFieldExistsAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)
	if err := addOriginalChecksumField(app); err != nil {
		t.Fatalf("addOriginalChecksumField on an already-migrated app: %v", err)
	}
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}
	if documents.Fields.GetByName("original_checksum") == nil {
		t.Fatal("documents.original_checksum is missing")
	}
	if documents.GetIndex("idx_documents_user_original_checksum") == "" {
		t.Fatal("the original_checksum lookup index is missing")
	}
}
