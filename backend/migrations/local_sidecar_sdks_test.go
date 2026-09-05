package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

// The failure this migration exists to prevent: EnsureCollection freezes the
// sdk field's allowed values when it first builds the collection and returns
// early ever after, so on an instance that has already booted, adding an SDK to
// ValidSDKs does nothing at all. Saving a provider with the new SDK then fails
// on the collection's own select validation -- an error about an invalid value,
// with nothing in it a reader could act on.
func TestWideningTheProviderSDKField(t *testing.T) {
	app := bootMigratedApp(t)

	// Rebuild the collection the way a pre-sidecar release left it.
	priorValues := []string{
		aiprovider.SDKOpenAI, aiprovider.SDKOpenRouter,
		aiprovider.SDKGoogleVision, aiprovider.SDKMistral,
	}
	collection, err := aiprovider.EnsureCollection(app)
	if err != nil {
		t.Fatalf("ensure collection: %v", err)
	}
	field, ok := collection.Fields.GetByName("sdk").(*core.SelectField)
	if !ok {
		t.Fatal("sdk is not a select field")
	}
	field.Values = priorValues
	if err := app.Save(collection); err != nil {
		t.Fatalf("narrow the field: %v", err)
	}

	// Both sidecar providers must be refused while the field is narrow, or
	// this test would pass without the migration doing anything.
	for _, sdk := range []string{aiprovider.SDKLocalEmbeddings, aiprovider.SDKDocling} {
		if err := saveSidecarProvider(app, sdk, sdk+"-before"); err == nil {
			t.Fatalf("a narrowed sdk field accepted a %s provider", sdk)
		}
	}

	if err := setProviderSDKValues(app, aiprovider.ValidSDKs); err != nil {
		t.Fatalf("widen: %v", err)
	}
	for _, sdk := range []string{aiprovider.SDKLocalEmbeddings, aiprovider.SDKDocling} {
		if err := saveSidecarProvider(app, sdk, sdk+"-after"); err != nil {
			t.Fatalf("save a %s provider after widening: %v", sdk, err)
		}
	}

	// priorSDKs is what the down-migration narrows back to, and it has to be
	// exactly the list rebuilt above -- derived from RequiresAPIKey rather than
	// written out, so a third sidecar needs no edit here.
	if !slices.Equal(priorSDKs(), priorValues) {
		t.Fatalf("priorSDKs() = %v, want %v", priorSDKs(), priorValues)
	}

	// Idempotent: managed instances re-run migrations on every boot, and this
	// one has to be a no-op once the values already match.
	if err := setProviderSDKValues(app, aiprovider.ValidSDKs); err != nil {
		t.Fatalf("re-widen: %v", err)
	}

	reloaded, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		t.Fatalf("reload collection: %v", err)
	}
	got := reloaded.Fields.GetByName("sdk").(*core.SelectField).Values
	if !slices.Equal(got, aiprovider.ValidSDKs) {
		t.Fatalf("sdk values = %v, want %v", got, aiprovider.ValidSDKs)
	}
}

// Migration order is not something this migration may depend on: it can run
// before whatever creates the providers collection, or on an instance where
// nothing has. Finding no collection is a no-op, not a failure -- and it stays
// correct because EnsureCollection then builds the field from the current list.
func TestWideningWithoutAProviderCollection(t *testing.T) {
	app := bootMigratedApp(t)
	if collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName); err == nil {
		if err := app.Delete(collection); err != nil {
			t.Fatalf("delete the providers collection: %v", err)
		}
	}

	if err := setProviderSDKValues(app, aiprovider.ValidSDKs); err != nil {
		t.Fatalf("widen with no collection: %v", err)
	}

	// And the list a later EnsureCollection builds is the current one, so
	// nothing is left needing a second migration.
	collection, err := aiprovider.EnsureCollection(app)
	if err != nil {
		t.Fatalf("ensure collection: %v", err)
	}
	got := collection.Fields.GetByName("sdk").(*core.SelectField).Values
	if !slices.Equal(got, aiprovider.ValidSDKs) {
		t.Fatalf("sdk values = %v, want %v", got, aiprovider.ValidSDKs)
	}
}

func saveSidecarProvider(app core.App, sdk, alias string) error {
	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		return err
	}
	record := core.NewRecord(collection)
	record.Set("sdk", sdk)
	record.Set("alias", alias)
	record.Set("base_url", aiprovider.DefaultBaseURL(sdk))
	return app.Save(record)
}
