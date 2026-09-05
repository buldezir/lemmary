package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// Adds ai_providers.oauth and widens ai_providers.sdk to include chatgpt.
//
// Two changes, one migration, because neither is usable without the other: the
// SDK cannot be selected until it is in the select field's values, and a row
// with that SDK holds no credential until the column exists.
//
// EnsureCollection already builds both from the current code, but it returns an
// existing collection untouched -- which is every install past its first boot.
// Same reasoning as 1730000024, which widened the field for the sidecar SDKs.
func init() {
	m.Register(func(app core.App) error {
		if err := addProviderOAuthField(app); err != nil {
			return err
		}
		return setProviderSDKValues(app, aiprovider.ValidSDKs)
	}, func(app core.App) error {
		// Rows before values, as in 1730000024: a record whose sdk is no longer
		// in Values fails validation on its next save. Best-effort throughout --
		// a down-migration that halts halfway is worse than one that leaves a
		// stale binding an admin can see and clear.
		records, err := app.FindAllRecords(aiprovider.CollectionName, dbx.HashExp{"sdk": aiprovider.SDKChatGPT})
		if err == nil {
			for _, record := range records {
				clearProviderBindings(app, record.Id)
				clearLLMProviderBindings(app, record.Id)
				_ = app.Delete(record)
			}
		}
		if err := dropProviderOAuthField(app); err != nil {
			return err
		}
		return setProviderSDKValues(app, sdksWithoutChatGPT())
	})
}

func sdksWithoutChatGPT() []string {
	out := make([]string, 0, len(aiprovider.ValidSDKs))
	for _, sdk := range aiprovider.ValidSDKs {
		if sdk != aiprovider.SDKChatGPT {
			out = append(out, sdk)
		}
	}
	return out
}

func addProviderOAuthField(app core.App) error {
	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		// Nothing to add on an instance that has never held a provider:
		// EnsureCollection builds the field itself. Migration order is not
		// something this may depend on.
		return nil
	}
	if collection.Fields.GetByName(aiprovider.OAuthField) != nil {
		return nil
	}
	collection.Fields.Add(&core.TextField{Name: aiprovider.OAuthField, Max: 8000})
	return app.Save(collection)
}

func dropProviderOAuthField(app core.App) error {
	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		return nil
	}
	if collection.Fields.GetByName(aiprovider.OAuthField) == nil {
		return nil
	}
	collection.Fields.RemoveByName(aiprovider.OAuthField)
	return app.Save(collection)
}

// clearLLMProviderBindings unbinds the four language-model roles when any of
// them points at the provider about to be deleted.
//
// Separate from clearProviderBindings, which covers OCR and embeddings: those
// two were the only roles the sidecar SDKs could hold, and a chatgpt provider
// can hold none of them and all four of these.
func clearLLMProviderBindings(app core.App, providerID string) {
	settings, err := app.FindRecordById("app_settings", "appsettings0001")
	if err != nil || settings == nil {
		return
	}
	changed := false
	for _, pair := range [][2]string{
		{"extract_provider_id", "extract_model"},
		{"chat_provider_id", "chat_model"},
		{"search_provider_id", "search_model"},
		{"search_helper_provider_id", "search_helper_model"},
	} {
		if settings.GetString(pair[0]) != providerID {
			continue
		}
		settings.Set(pair[0], "")
		settings.Set(pair[1], "")
		changed = true
	}
	if changed {
		_ = app.Save(settings)
	}
}
