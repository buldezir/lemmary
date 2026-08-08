package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		field := collection.Fields.GetByName("file")
		fileField, ok := field.(*core.FileField)
		if !ok || fileField == nil {
			return nil
		}
		fileField.MimeTypes = []string{
			"application/pdf",
			"image/jpeg",
			"image/png",
			"image/webp",
			"text/plain",
			"text/csv",
			"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
			"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return nil
		}
		field := collection.Fields.GetByName("file")
		fileField, ok := field.(*core.FileField)
		if !ok || fileField == nil {
			return nil
		}
		fileField.MimeTypes = []string{
			"application/pdf",
			"image/jpeg",
			"image/png",
			"image/webp",
			"text/plain",
		}
		return app.Save(collection)
	})
}
