package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// Widens ai_providers.sdk to the local OCR sidecar, docling.
//
// aiprovider.EnsureCollection builds that select field's values from ValidSDKs,
// but it returns an existing collection untouched -- which is every install
// past its first boot. Without this, adding a Docling provider from Settings
// fails PocketBase's own select validation, with a message that names no SDK at
// all and no way for an admin to act on it.
func init() {
	m.Register(func(app core.App) error {
		field, collection, err := providerSDKField(app)
		if err != nil || field == nil {
			// No collection yet: a fresh database gets the current list from
			// EnsureCollection instead.
			return err
		}
		// Assigned, not appended: ValidSDKs is the list this build knows how to
		// serve, and appending would keep a value it has forgotten.
		field.Values = append([]string(nil), aiprovider.ValidSDKs...)
		return app.Save(collection)
	}, func(app core.App) error {
		field, collection, err := providerSDKField(app)
		if err != nil || field == nil {
			return err
		}
		// Rows before values. A record whose sdk is no longer in Values fails
		// validation on its next save, which would strand an install between
		// this migration and the one before it -- and the settings row pointing
		// at a deleted provider would dangle. Best-effort throughout: a
		// down-migration that halts halfway is worse than one that leaves a
		// stale binding an admin can see and clear.
		records, err := app.FindAllRecords(aiprovider.CollectionName,
			dbx.HashExp{"sdk": aiprovider.SDKDocling})
		if err == nil {
			for _, record := range records {
				clearOCRBinding(app, record.Id)
				_ = app.Delete(record)
			}
		}
		field.Values = []string{
			aiprovider.SDKOpenAI,
			aiprovider.SDKOpenRouter,
			aiprovider.SDKGoogleVision,
			aiprovider.SDKMistral,
		}
		return app.Save(collection)
	})
}

func providerSDKField(app core.App) (*core.SelectField, *core.Collection, error) {
	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		return nil, nil, nil
	}
	field, ok := collection.Fields.GetByName("sdk").(*core.SelectField)
	if !ok || field == nil {
		return nil, nil, nil
	}
	return field, collection, nil
}

// clearOCRBinding unbinds OCR when it points at the provider about to be
// deleted, so the settings row does not keep an id that resolves to nothing.
func clearOCRBinding(app core.App, providerID string) {
	settings, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil || settings == nil {
		return
	}
	if settings.GetString("ocr_provider_id") != providerID {
		return
	}
	settings.Set("ocr_provider_id", "")
	settings.Set("ocr_model", "")
	_ = app.Save(settings)
}
