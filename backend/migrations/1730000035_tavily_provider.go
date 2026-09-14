package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// Widens ai_providers.sdk to include tavily, and adds the app_settings column
// binding the provider that backs the web_search and web_fetch tools.
//
// The widening is the same reasoning as 1730000024, 1730000025 and 1730000026:
// EnsureCollection builds the select field from ValidSDKs but returns an
// existing collection untouched, so on any instance past its first boot saving
// a tavily row would fail PocketBase's own select validation.
//
// No model column beside the binding: a web-search API takes no model.
func init() {
	m.Register(func(app core.App) error {
		// Values before rows, as in 1730000026.
		if err := setProviderSDKValues(app, aiprovider.ValidSDKs); err != nil {
			return err
		}
		return addWebSearchBinding(app)
	}, func(app core.App) error {
		// Rows before values: a record whose sdk is no longer in Values fails
		// validation on its next save, so the binding goes first and any tavily
		// row with it.
		if err := removeWebSearchBinding(app); err != nil {
			return err
		}
		return setProviderSDKValues(app, sdksWithoutTavily())
	})
}

func sdksWithoutTavily() []string {
	out := make([]string, 0, len(aiprovider.ValidSDKs))
	for _, sdk := range aiprovider.ValidSDKs {
		if sdk != aiprovider.SDKTavily {
			out = append(out, sdk)
		}
	}
	return out
}

func addWebSearchBinding(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return err
	}
	if collection.Fields.GetByName("websearch_provider_id") == nil {
		collection.Fields.Add(&core.TextField{Name: "websearch_provider_id", Max: 15})
	}
	return app.Save(collection)
}

func removeWebSearchBinding(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		return nil
	}
	if f := collection.Fields.GetByName("websearch_provider_id"); f != nil {
		collection.Fields.RemoveById(f.GetId())
	}
	return app.Save(collection)
}
