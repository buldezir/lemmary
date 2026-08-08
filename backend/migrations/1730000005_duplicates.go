package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

func init() {
	m.Register(func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err != nil {
			return err
		}
		if documents.Fields.GetByName("checksum") == nil {
			documents.Fields.Add(&core.TextField{Name: "checksum", Max: 64})
		}
		if documents.Fields.GetByName("text_fingerprint") == nil {
			documents.Fields.Add(&core.TextField{Name: "text_fingerprint", Max: 16})
		}
		if documents.Fields.GetByName("duplicate_of") == nil {
			documents.Fields.Add(&core.RelationField{
				Name:         "duplicate_of",
				CollectionId: documents.Id,
				MaxSelect:    1,
			})
		}
		// Unique per user when checksum is set so concurrent identical uploads cannot both insert.
		documents.RemoveIndex("idx_documents_checksum")
		documents.AddIndex("idx_documents_user_checksum", true, "user, checksum", "checksum != ''")
		if err := app.Save(documents); err != nil {
			return err
		}

		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		if settings.Fields.GetByName("near_duplicate_detection_enabled") == nil {
			settings.Fields.Add(&core.BoolField{Name: "near_duplicate_detection_enabled"})
		}
		if settings.Fields.GetByName("near_duplicate_threshold") == nil {
			settings.Fields.Add(&core.NumberField{Name: "near_duplicate_threshold"})
		}
		return app.Save(settings)
	}, func(app core.App) error {
		documents, err := app.FindCollectionByNameOrId("documents")
		if err == nil {
			if f := documents.Fields.GetByName("checksum"); f != nil {
				documents.Fields.RemoveById(f.GetId())
			}
			if f := documents.Fields.GetByName("text_fingerprint"); f != nil {
				documents.Fields.RemoveById(f.GetId())
			}
			if f := documents.Fields.GetByName("duplicate_of"); f != nil {
				documents.Fields.RemoveById(f.GetId())
			}
			documents.RemoveIndex("idx_documents_checksum")
			documents.RemoveIndex("idx_documents_user_checksum")
			_ = app.Save(documents)
		}

		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		if f := settings.Fields.GetByName("near_duplicate_detection_enabled"); f != nil {
			settings.Fields.RemoveById(f.GetId())
		}
		if f := settings.Fields.GetByName("near_duplicate_threshold"); f != nil {
			settings.Fields.RemoveById(f.GetId())
		}
		return app.Save(settings)
	})
}
