package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

// The same failure 1730000024 exists to prevent, one SDK later: the sdk field's
// allowed values are frozen when EnsureCollection first builds the collection,
// so on an instance that has already booted an anthropic row would be refused
// by PocketBase's own select validation with nothing actionable in the message.
func TestAnAnthropicProviderSavesAfterTheMigration(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		t.Fatalf("find the providers collection: %v", err)
	}
	values := collection.Fields.GetByName("sdk").(*core.SelectField).Values
	if !slices.Contains(values, aiprovider.SDKAnthropic) {
		t.Fatalf("sdk values = %v, want anthropic among them", values)
	}

	record := core.NewRecord(collection)
	record.Set("sdk", aiprovider.SDKAnthropic)
	record.Set("alias", aiprovider.DefaultAlias(aiprovider.SDKAnthropic))
	record.Set("base_url", aiprovider.DefaultBaseURL(aiprovider.SDKAnthropic))
	record.Set("catalog", aiprovider.DefaultCatalog(aiprovider.SDKAnthropic))
	record.Set("api_key", "sk-ant-test")
	if err := app.Save(record); err != nil {
		t.Fatalf("save an anthropic provider: %v", err)
	}
}

// What the down-migration narrows back to. Derived rather than written out, so
// an eleventh SDK needs no edit here.
func TestSDKsWithoutAnthropicDropsOnlyAnthropic(t *testing.T) {
	t.Parallel()
	got := sdksWithoutAnthropic()
	if slices.Contains(got, aiprovider.SDKAnthropic) {
		t.Fatalf("sdksWithoutAnthropic() = %v, want anthropic gone", got)
	}
	if len(got) != len(aiprovider.ValidSDKs)-1 {
		t.Fatalf("sdksWithoutAnthropic() = %v, want every other SDK kept", got)
	}
	for _, sdk := range aiprovider.ValidSDKs {
		if sdk != aiprovider.SDKAnthropic && !slices.Contains(got, sdk) {
			t.Errorf("%s was dropped too", sdk)
		}
	}
}
