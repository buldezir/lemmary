package migrations

import (
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// Widen ai_providers.sdk to whatever aiprovider.ValidSDKs now lists.
//
// EnsureCollection builds that field as a SelectField with Values: ValidSDKs,
// but it returns early when the collection already exists -- so adding an SDK
// to the slice does nothing on any instance that has already booted, and saving
// a provider with the new SDK fails the collection's own validation with a
// message about an invalid select value rather than anything a reader could act
// on. This is the migration that makes SDKLocal reachable on an existing
// install.
//
// Written against ValidSDKs rather than a literal so the next SDK needs no new
// migration body: re-running this is a no-op when the values already match.
func init() {
	m.Register(func(app core.App) error {
		return setProviderSDKValues(app, aiprovider.ValidSDKs)
	}, func(app core.App) error {
		// Narrow back to the SDKs that existed before. A local provider row
		// left behind would then fail its next save, which is the honest
		// outcome of rolling back the feature that created it.
		prior := slices.DeleteFunc(slices.Clone(aiprovider.ValidSDKs), func(sdk string) bool {
			return sdk == aiprovider.SDKLocal
		})
		return setProviderSDKValues(app, prior)
	})
}

func setProviderSDKValues(app core.App, values []string) error {
	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		// Nothing to widen on an instance that has never held a provider:
		// EnsureCollection builds the field from the current list.
		return nil
	}
	field, ok := collection.Fields.GetByName("sdk").(*core.SelectField)
	if !ok {
		return nil
	}
	if slices.Equal(field.Values, values) {
		return nil
	}
	field.Values = values
	return app.Save(collection)
}
