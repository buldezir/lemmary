package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// The sixth task binding: the model Deep Search hands bulk per-document work to.
// Separate because that work is many cheap calls where the research loop itself
// is a few expensive ones. Unset falls back to the search model.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("search_helper_provider_id") == nil {
			collection.Fields.Add(&core.TextField{Name: "search_helper_provider_id", Max: 15})
		}
		if collection.Fields.GetByName("search_helper_model") == nil {
			collection.Fields.Add(&core.TextField{Name: "search_helper_model", Max: 200})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		for _, name := range []string{"search_helper_provider_id", "search_helper_model"} {
			if f := collection.Fields.GetByName(name); f != nil {
				collection.Fields.RemoveById(f.GetId())
			}
		}
		return app.Save(collection)
	})
}
