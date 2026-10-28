package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

func TestALemmaryLocalAIProviderSavesAfterTheMigration(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		t.Fatalf("find the providers collection: %v", err)
	}
	values := collection.Fields.GetByName("sdk").(*core.SelectField).Values
	if !slices.Contains(values, aiprovider.SDKLemmaryLocalAI) {
		t.Fatalf("sdk values = %v, want lemmary_local_ai among them", values)
	}

	record := core.NewRecord(collection)
	record.Set("sdk", aiprovider.SDKLemmaryLocalAI)
	record.Set("alias", aiprovider.DefaultAlias(aiprovider.SDKLemmaryLocalAI))
	record.Set("base_url", "http://192.168.1.20:5001/v1")
	if err := app.Save(record); err != nil {
		t.Fatalf("save a lemmary_local_ai provider: %v", err)
	}
}
