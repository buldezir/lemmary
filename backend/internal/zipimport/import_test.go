package zipimport

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/testpb"
	_ "lemmary/backend/migrations"
)

// The storage layer refuses non-UTF-8 metadata. Windows zips carry names in the
// DOS code page ("Lämmäry" as "L\x84mm\x84ry"), and PocketBase cuts a long
// name at 255 bytes, which can split a rune.
func TestCreateDocumentKeepsTheOriginalNameStorable(t *testing.T) {
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

	long := strings.Repeat("ä", 200) + ".txt"
	for name, want := range map[string]string{
		"L\x84mm\x84ry.txt": "Lmmry.txt",
		"Lämmäry.txt":       "Lämmäry.txt",
		long:                long[:254],
	} {
		file, err := filesystem.NewFileFromBytes([]byte("hello "+name), name)
		if err != nil {
			t.Fatal(err)
		}
		if err := CreateDocument(app, collection, user.Id, file, nil); err != nil {
			t.Fatalf("create %q: %v", name, err)
		}
		if file.OriginalName != want || !utf8.ValidString(file.OriginalName) {
			t.Fatalf("original name of %q = %q, want %q", name, file.OriginalName, want)
		}
	}
}
