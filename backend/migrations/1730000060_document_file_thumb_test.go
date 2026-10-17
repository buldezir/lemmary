package migrations

import (
	"slices"
	"testing"
)

func TestDocumentFileThumbMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	for range 2 {
		if err := addDocumentFileThumb(app); err != nil {
			t.Fatalf("addDocumentFileThumb on an already-migrated app: %v", err)
		}
	}
	file, _, err := documentFileField(app)
	if err != nil {
		t.Fatalf("documents.file: %v", err)
	}
	declared := slices.DeleteFunc(slices.Clone(file.Thumbs), func(size string) bool { return size != documentFileThumb })
	if len(declared) != 1 {
		t.Fatalf("documents.file thumbs %v: want %q exactly once", file.Thumbs, documentFileThumb)
	}
}
