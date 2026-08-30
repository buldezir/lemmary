package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/models"
)

func init() {
	m.Register(func(app core.App) error {
		authRule := "@request.auth.id != ''"

		tags := core.NewBaseCollection("tags")
		tags.ListRule = types.Pointer(authRule)
		tags.ViewRule = types.Pointer(authRule)
		tags.CreateRule = types.Pointer(authRule)
		tags.UpdateRule = types.Pointer(authRule)
		tags.DeleteRule = types.Pointer(authRule)
		tags.Fields.Add(
			&core.TextField{Name: "name", Required: true, Max: 100},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		tags.AddIndex("idx_tags_name", true, "name", "")
		if err := app.Save(tags); err != nil {
			return err
		}

		correspondents := core.NewBaseCollection("correspondents")
		correspondents.ListRule = types.Pointer(authRule)
		correspondents.ViewRule = types.Pointer(authRule)
		correspondents.CreateRule = types.Pointer(authRule)
		correspondents.UpdateRule = types.Pointer(authRule)
		correspondents.DeleteRule = types.Pointer(authRule)
		correspondents.Fields.Add(
			&core.TextField{Name: "name", Required: true, Max: 255},
			&core.TextField{Name: "name_original", Max: 255},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		correspondents.AddIndex("idx_correspondents_name", true, "name", "")
		if err := app.Save(correspondents); err != nil {
			return err
		}

		documentTypes := core.NewBaseCollection("document_types")
		documentTypes.ListRule = types.Pointer(authRule)
		documentTypes.ViewRule = types.Pointer(authRule)
		documentTypes.CreateRule = types.Pointer(authRule)
		documentTypes.UpdateRule = types.Pointer(authRule)
		documentTypes.DeleteRule = types.Pointer(authRule)
		documentTypes.Fields.Add(
			&core.TextField{Name: "name", Required: true, Max: 255},
			&core.TextField{Name: "name_original", Max: 255},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		documentTypes.AddIndex("idx_document_types_name", true, "name", "")
		if err := app.Save(documentTypes); err != nil {
			return err
		}

		ownerRule := "user = @request.auth.id"
		documents := core.NewBaseCollection("documents")
		documents.ListRule = types.Pointer(ownerRule)
		documents.ViewRule = types.Pointer(ownerRule)
		documents.CreateRule = types.Pointer(ownerRule)
		documents.UpdateRule = types.Pointer(ownerRule)
		documents.DeleteRule = types.Pointer(ownerRule)
		documents.Fields.Add(
			&core.FileField{
				Name:      "file",
				Required:  true,
				MaxSelect: 1,
				MaxSize:   20 << 20,
				MimeTypes: []string{
					"application/pdf",
					"image/jpeg",
					"image/png",
					"image/webp",
					"text/plain",
				},
			},
			&core.RelationField{
				Name:         "user",
				Required:     true,
				CollectionId: "_pb_users_auth_",
				MaxSelect:    1,
			},
			&core.TextField{Name: "title", Max: 500},
			&core.TextField{Name: "title_original", Max: 500},
			&core.TextField{Name: "purpose", Max: 1000},
			&core.TextField{Name: "purpose_original", Max: 1000},
			&core.DateField{Name: "document_date"},
			&core.RelationField{
				Name:         "document_type",
				CollectionId: documentTypes.Id,
				MaxSelect:    1,
			},
			&core.RelationField{
				Name:         "correspondent",
				CollectionId: correspondents.Id,
				MaxSelect:    1,
			},
			&core.TextField{Name: "ocr_text", Max: models.MaxOCRTextRunes},
			&core.TextField{Name: "summary", Max: 5000},
			&core.TextField{Name: "summary_original", Max: 5000},
			&core.SelectField{
				Name:   "processing_status",
				Values: []string{"pending", "processing", "completed", "failed", "needs_review"},
			},
			&core.TextField{Name: "metadata_source", Max: 200},
			&core.NumberField{Name: "confidence", Min: types.Pointer(0.0), Max: types.Pointer(1.0)},
			&core.JSONField{Name: "people_or_organizations"},
			&core.RelationField{
				Name:         "tags",
				CollectionId: tags.Id,
				MaxSelect:    50,
			},
			&core.FileField{
				Name:      "preview",
				Required:  false,
				MaxSelect: 1,
				MaxSize:   2 << 20,
				MimeTypes: []string{
					"image/png",
				},
			},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		if err := app.Save(documents); err != nil {
			return err
		}

		jobs := core.NewBaseCollection("processing_jobs")
		jobs.ListRule = types.Pointer("document.user = @request.auth.id")
		jobs.ViewRule = types.Pointer("document.user = @request.auth.id")
		jobs.CreateRule = types.Pointer(`document.user = @request.auth.id && status = "pending"`)
		jobs.UpdateRule = nil
		jobs.DeleteRule = nil
		jobs.Fields.Add(
			&core.RelationField{
				Name:         "document",
				Required:     true,
				CollectionId: documents.Id,
				MaxSelect:    1,
			},
			&core.SelectField{
				Name:     "status",
				Required: true,
				Values:   []string{"pending", "running", "completed", "failed", "needs_review"},
			},
			&core.TextField{Name: "task_id", Max: 36},
			&core.JSONField{Name: "steps"},
			&core.JSONField{Name: "step_runs"},
			&core.TextField{Name: "current_step", Max: 50},
			&core.JSONField{Name: "metadata_json"},
			&core.JSONField{Name: "force_steps"},
			&core.DateField{Name: "started_at"},
			&core.DateField{Name: "finished_at"},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		jobs.AddIndex("idx_processing_jobs_task_id", false, "task_id", "")

		return app.Save(jobs)
	}, func(app core.App) error {
		for _, name := range []string{"processing_jobs", "documents", "document_types", "correspondents", "tags"} {
			collection, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				continue
			}
			if err := app.Delete(collection); err != nil {
				return err
			}
		}
		return nil
	})
}
