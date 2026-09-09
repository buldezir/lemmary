package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// The provider and model a reprocess job runs on, when it was queued on
// something other than the configured bindings.
//
// One JSON column rather than six text ones. The shape is
//
//	{"ocr":{"provider_id":"...","model":"..."},
//	 "extract":{...},"embedding":{...}}
//
// which is worth a column of its own precisely because the set of bindings
// keeps growing: app_settings has taken four separate migrations to reach six
// pairs, and a job carrying the overrides as a document needs none of them.
//
// Empty or absent means the Settings bindings, which is every job that already
// exists and every job queued without a picker touched -- so this changes no
// behaviour by itself. The column is validated in the processing_jobs create
// hook rather than in the reprocess handler, because the single-document
// reprocess form writes a job straight through the collection API.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("overrides") == nil {
			collection.Fields.Add(&core.JSONField{Name: "overrides", MaxSize: 2000})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("processing_jobs")
		if err != nil {
			return nil
		}
		if f := collection.Fields.GetByName("overrides"); f != nil {
			collection.Fields.RemoveById(f.GetId())
		}
		return app.Save(collection)
	})
}
