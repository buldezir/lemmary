package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Drop search_context_tokens. Research no longer estimates a context window
// or stops gathering against one: a run that outgrows the model is a provider
// error, the same as any other completion that does not fit.
func init() {
	m.Register(func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		settings.Fields.RemoveByName("search_context_tokens")
		return app.Save(settings)
	}, func(app core.App) error {
		settings, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		if settings.Fields.GetByName("search_context_tokens") == nil {
			settings.Fields.Add(&core.NumberField{Name: "search_context_tokens", OnlyInt: true, Min: types.Pointer(0.0)})
		}
		return app.Save(settings)
	})
}
