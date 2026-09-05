package migrations

import (
	"slices"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// Widens ai_providers.sdk to the two sidecar SDKs, docling and local.
//
// aiprovider.EnsureCollection builds that select field's values from ValidSDKs,
// but it returns an existing collection untouched -- which is every install
// past its first boot. Without this, adding a sidecar provider from Settings
// fails PocketBase's own select validation, with a message that names no SDK at
// all and no way for an admin to act on it.
//
// Written against ValidSDKs rather than a literal so the next SDK needs no new
// migration body, and idempotent so a managed instance can re-run it on every
// boot.
func init() {
	m.Register(func(app core.App) error {
		return setProviderSDKValues(app, aiprovider.ValidSDKs)
	}, func(app core.App) error {
		// Rows before values. A record whose sdk is no longer in Values fails
		// validation on its next save, which would strand an install between
		// this migration and the one before it -- and a settings row pointing
		// at a deleted provider would dangle. Best-effort throughout: a
		// down-migration that halts halfway is worse than one that leaves a
		// stale binding an admin can see and clear.
		for _, sdk := range []string{aiprovider.SDKDocling, aiprovider.SDKLocalEmbeddings} {
			records, err := app.FindAllRecords(aiprovider.CollectionName, dbx.HashExp{"sdk": sdk})
			if err != nil {
				continue
			}
			for _, record := range records {
				clearProviderBindings(app, record.Id)
				_ = app.Delete(record)
			}
		}
		return setProviderSDKValues(app, priorSDKs())
	})
}

// priorSDKs is ValidSDKs without the sidecars: the list as it stood before
// either of them existed. Derived so it cannot go stale against ValidSDKs the
// way a written-out literal would.
func priorSDKs() []string {
	return slices.DeleteFunc(slices.Clone(aiprovider.ValidSDKs), func(sdk string) bool {
		return !aiprovider.RequiresAPIKey(sdk)
	})
}

func setProviderSDKValues(app core.App, values []string) error {
	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		// Nothing to widen on an instance that has never held a provider:
		// EnsureCollection builds the field from the current list. Migration
		// order is not something this may depend on.
		return nil
	}
	field, ok := collection.Fields.GetByName("sdk").(*core.SelectField)
	if !ok {
		return nil
	}
	if slices.Equal(field.Values, values) {
		return nil
	}
	field.Values = append([]string(nil), values...)
	return app.Save(collection)
}

// clearProviderBindings unbinds OCR and embeddings when either points at the
// provider about to be deleted, so the settings row does not keep an id that
// resolves to nothing.
func clearProviderBindings(app core.App, providerID string) {
	settings, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil || settings == nil {
		return
	}
	changed := false
	if settings.GetString("ocr_provider_id") == providerID {
		settings.Set("ocr_provider_id", "")
		settings.Set("ocr_model", "")
		changed = true
	}
	if settings.GetString("embedding_provider_id") == providerID {
		settings.Set("embedding_provider_id", "")
		settings.Set("embedding_model", "")
		settings.Set("embedding_dims", 0)
		changed = true
	}
	if changed {
		_ = app.Save(settings)
	}
}
