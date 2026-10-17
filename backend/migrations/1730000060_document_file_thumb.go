package migrations

import (
	"fmt"
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// The card grid's picture of an image document. A PDF gets a rendered preview
// field from the pipeline; an image only needs scaling, which PocketBase does
// itself for a size the field declares, on first request, cached beside the
// original. The frontend asks for exactly this size (lib/documentPreview.ts):
// PocketBase serves the original for one the field does not declare.
const documentFileThumb = "400x0"

func init() {
	m.Register(addDocumentFileThumb, func(app core.App) error {
		file, documents, err := documentFileField(app)
		if err != nil {
			return err
		}
		file.Thumbs = slices.DeleteFunc(file.Thumbs, func(size string) bool { return size == documentFileThumb })
		return app.Save(documents)
	})
}

func addDocumentFileThumb(app core.App) error {
	file, documents, err := documentFileField(app)
	if err != nil {
		return err
	}
	if slices.Contains(file.Thumbs, documentFileThumb) {
		return nil
	}
	file.Thumbs = append(file.Thumbs, documentFileThumb)
	return app.Save(documents)
}

func documentFileField(app core.App) (*core.FileField, *core.Collection, error) {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return nil, nil, err
	}
	file, ok := documents.Fields.GetByName("file").(*core.FileField)
	if !ok {
		return nil, nil, fmt.Errorf("documents.file is not a file field")
	}
	return file, documents, nil
}
