package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Research mode reads documents until the model's context window is spent, so
// the window has to be a stored setting: it is the only thing bounding a run.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("search_context_tokens") == nil {
			collection.Fields.Add(&core.NumberField{Name: "search_context_tokens", OnlyInt: true, Min: types.Pointer(0.0)})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		if f := collection.Fields.GetByName("search_context_tokens"); f != nil {
			collection.Fields.RemoveById(f.GetId())
		}
		return app.Save(collection)
	})
}
