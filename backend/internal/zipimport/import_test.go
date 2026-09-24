package zipimport

import (
	"testing"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/testpb"
	_ "lemmary/backend/migrations"
)

// Windows zips carry entry names in the DOS code page, e.g. "Lämmäry" as
// "L\x84mm\x84ry", which the storage layer refuses as metadata.
func TestCreateDocumentStripsInvalidUTF8FromTheName(t *testing.T) {
	app := testpb.Open(t)
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatal(err)
	}
	user := core.NewRecord(users)
	user.Set("email", "owner@example.com")
	user.SetPassword("test-password-123")
	if err := app.Save(user); err != nil {
		t.Fatal(err)
	}
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatal(err)
	}

	file, err := filesystem.NewFileFromBytes([]byte("hello"), "L\x84mm\x84ry.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateDocument(app, collection, user.Id, file, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if file.OriginalName != "Lmmry.txt" || !utf8.ValidString(file.OriginalName) {
		t.Fatalf("original name = %q", file.OriginalName)
	}
}
