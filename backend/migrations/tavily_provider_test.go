package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

// The same failure 1730000024 exists to prevent, one SDK later: the sdk field's
// allowed values are frozen when EnsureCollection first builds the collection,
// so on an instance that has already booted a tavily row would be refused by
// PocketBase's own select validation with nothing actionable in the message.
func TestATavilyProviderSavesAfterTheMigration(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		t.Fatalf("find the providers collection: %v", err)
	}
	values := collection.Fields.GetByName("sdk").(*core.SelectField).Values
	if !slices.Contains(values, aiprovider.SDKTavily) {
		t.Fatalf("sdk values = %v, want tavily among them", values)
	}

	record := core.NewRecord(collection)
	record.Set("sdk", aiprovider.SDKTavily)
	record.Set("alias", aiprovider.DefaultAlias(aiprovider.SDKTavily))
	record.Set("base_url", aiprovider.DefaultBaseURL(aiprovider.SDKTavily))
	record.Set("api_key", "tvly-test")
	if err := app.Save(record); err != nil {
		t.Fatalf("save a tavily provider: %v", err)
	}
}

// The binding column is what Settings writes and config.Load reads; without it
// a bound provider would be silently forgotten on every save.
func TestTheWebSearchBindingColumnExists(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("find app_settings: %v", err)
	}
	if collection.Fields.GetByName("websearch_provider_id") == nil {
		t.Fatal("app_settings has no websearch_provider_id column")
	}

	// Managed instances re-run migrations on every boot, so adding it twice has
	// to be a no-op rather than a duplicate field.
	if err := addWebSearchBinding(app); err != nil {
		t.Fatalf("re-add the binding column: %v", err)
	}
	reloaded, err := app.FindCollectionByNameOrId("app_settings")
	if err != nil {
		t.Fatalf("reload app_settings: %v", err)
	}
	count := 0
	for _, field := range reloaded.Fields {
		if field.GetName() == "websearch_provider_id" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("websearch_provider_id appears %d times, want once", count)
	}
}

// What the down-migration narrows back to. Derived rather than written out, so
// a tenth SDK needs no edit here.
func TestSDKsWithoutTavilyDropsOnlyTavily(t *testing.T) {
	t.Parallel()
	got := sdksWithoutTavily()
	if slices.Contains(got, aiprovider.SDKTavily) {
		t.Fatalf("sdksWithoutTavily() = %v, want tavily gone", got)
	}
	if len(got) != len(aiprovider.ValidSDKs)-1 {
		t.Fatalf("sdksWithoutTavily() = %v, want every other SDK kept", got)
	}
	for _, sdk := range aiprovider.ValidSDKs {
		if sdk != aiprovider.SDKTavily && !slices.Contains(got, sdk) {
			t.Errorf("%s was dropped too", sdk)
		}
	}
}
