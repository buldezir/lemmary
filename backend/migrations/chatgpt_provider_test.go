package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/chatgpt"
)

// A chatgpt provider needs two things that no earlier release's collection has:
// its SDK in the sdk field's allowed values, and a column to keep the token in.
// Either one missing fails the save, so both are asserted through a real save
// rather than by reading the schema back.
func TestAChatGPTProviderCanBeSavedAfterMigrating(t *testing.T) {
	app := bootMigratedApp(t)

	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		t.Fatalf("providers collection: %v", err)
	}
	if collection.Fields.GetByName(aiprovider.OAuthField) == nil {
		t.Fatal("the oauth column is missing after migrating")
	}

	token, err := chatgpt.Token{Access: "a", Refresh: "r"}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	record := core.NewRecord(collection)
	record.Set("sdk", aiprovider.SDKChatGPT)
	record.Set("alias", aiprovider.DefaultAlias(aiprovider.SDKChatGPT))
	record.Set("base_url", aiprovider.DefaultBaseURL(aiprovider.SDKChatGPT))
	record.Set(aiprovider.OAuthField, token)
	if err := app.Save(record); err != nil {
		t.Fatalf("save a chatgpt provider: %v", err)
	}

	// And it reads back as signed in, which is what Configured asks and what
	// the runtime builds a client on.
	p := aiprovider.FromRecord(record)
	if !p.Configured() {
		t.Fatal("a saved chatgpt provider with a token reads as unconfigured")
	}
}

// Managed instances re-run migrations on every boot, so both halves have to be
// no-ops once they have been applied.
func TestTheChatGPTMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)

	for i := 0; i < 2; i++ {
		if err := addProviderOAuthField(app); err != nil {
			t.Fatalf("add the oauth field (pass %d): %v", i, err)
		}
		if err := setProviderSDKValues(app, aiprovider.ValidSDKs); err != nil {
			t.Fatalf("widen the sdk field (pass %d): %v", i, err)
		}
	}
}

// The down-migration narrows the field back to the list as it stood before
// chatgpt existed. Derived rather than written out, so the next SDK needs no
// edit here -- and it must not drop anything else on the way.
func TestNarrowingBackRemovesOnlyChatGPT(t *testing.T) {
	got := sdksWithoutChatGPT()
	if len(got) != len(aiprovider.ValidSDKs)-1 {
		t.Fatalf("prior SDKs = %v, want every SDK but chatgpt", got)
	}
	for _, sdk := range got {
		if sdk == aiprovider.SDKChatGPT {
			t.Fatal("the narrowed list still names chatgpt")
		}
	}
}
