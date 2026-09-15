package migrations

import (
	"slices"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chat"
)

func TestTheProviderCatalogColumnExistsAndAcceptsACatalogue(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		t.Fatalf("find the providers collection: %v", err)
	}
	field, ok := collection.Fields.GetByName("catalog").(*core.SelectField)
	if !ok {
		t.Fatal("catalog field is missing or not a select")
	}
	if !slices.Contains(field.Values, "groq") {
		t.Fatalf("catalog values = %v, want the pi.dev list", field.Values)
	}

	record := core.NewRecord(collection)
	record.Set("sdk", aiprovider.SDKOpenAI)
	record.Set("alias", "groq-row")
	record.Set("base_url", "https://api.groq.com/openai/v1")
	record.Set("api_key", "test-key")
	record.Set("catalog", "groq")
	if err := app.Save(record); err != nil {
		t.Fatalf("save a provider on the groq catalogue: %v", err)
	}
	if got := aiprovider.FromRecord(record).Catalog; got != "groq" {
		t.Fatalf("catalog read back as %q", got)
	}
}

// A window is looked up per model, so the column the number is stored against
// has to survive a fresh migration run as well.
func TestTheChatMessageUsageColumnExists(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
	if err != nil {
		t.Fatalf("find the messages collection: %v", err)
	}
	field, ok := collection.Fields.GetByName("usage").(*core.JSONField)
	if !ok {
		t.Fatal("usage field is missing or not JSON")
	}
	if field.MaxSize != chat.MaxUsageJSONBytes {
		t.Fatalf("usage MaxSize = %d, want %d", field.MaxSize, chat.MaxUsageJSONBytes)
	}
}
