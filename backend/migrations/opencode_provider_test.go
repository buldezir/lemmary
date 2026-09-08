package migrations

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/aiprovider"
)

func saveProvider(t *testing.T, app core.App, sdk, alias, baseURL string) *core.Record {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
	if err != nil {
		t.Fatalf("providers collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("sdk", sdk)
	record.Set("alias", alias)
	record.Set("base_url", baseURL)
	record.Set("api_key", "test-key")
	if err := app.Save(record); err != nil {
		t.Fatalf("save a %s provider: %v", sdk, err)
	}
	return record
}

// The rows this migration exists for: before SDKOpenCode the only way to reach
// OpenCode was an openai row with a base URL, which is what .env.example
// shipped. Left behind, such a row stops sending the x-opencode-session header
// -- which gets requests refused -- and loses its endpoint routing.
func TestOpenCodeRowsAreMovedOntoTheSDK(t *testing.T) {
	app := bootMigratedApp(t)

	moved := saveProvider(t, app, aiprovider.SDKOpenAI, "Zen", "https://opencode.ai/zen/go/v1")
	viaRouter := saveProvider(t, app, aiprovider.SDKOpenRouter, "Zen via OpenRouter", "https://api.opencode.ai/zen/go/v1")
	// Left alone: a real OpenAI row, and one whose path merely mentions the name.
	kept := saveProvider(t, app, aiprovider.SDKOpenAI, "OpenAI", "https://api.openai.com/v1")
	lookalike := saveProvider(t, app, aiprovider.SDKOpenAI, "Lookalike", "https://gateway.example.com/opencode.ai/v1")

	if err := moveOpenCodeRows(app, openCodeCapableSDKs(), aiprovider.SDKOpenCode); err != nil {
		t.Fatalf("move: %v", err)
	}

	for _, tc := range []struct {
		record *core.Record
		want   string
	}{
		{moved, aiprovider.SDKOpenCode},
		{viaRouter, aiprovider.SDKOpenCode},
		{kept, aiprovider.SDKOpenAI},
		{lookalike, aiprovider.SDKOpenAI},
	} {
		reloaded, err := app.FindRecordById(aiprovider.CollectionName, tc.record.Id)
		if err != nil {
			t.Fatalf("reload %s: %v", tc.record.GetString("alias"), err)
		}
		if got := reloaded.GetString("sdk"); got != tc.want {
			t.Errorf("%s: sdk = %q, want %q", tc.record.GetString("alias"), got, tc.want)
		}
		// The credential and the alias are the operator's; only the SDK moves.
		if reloaded.GetString("api_key") != "test-key" {
			t.Errorf("%s: the API key did not survive the move", tc.record.GetString("alias"))
		}
	}
}

// Managed instances re-run migrations on every boot, so both halves have to be
// no-ops once applied -- and the up half has to be safe on an install that
// already had an opencode row.
func TestTheOpenCodeMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)
	record := saveProvider(t, app, aiprovider.SDKOpenAI, "Zen", "https://opencode.ai/zen/go/v1")

	for i := 0; i < 2; i++ {
		if err := setProviderSDKValues(app, aiprovider.ValidSDKs); err != nil {
			t.Fatalf("widen (pass %d): %v", i, err)
		}
		if err := moveOpenCodeRows(app, openCodeCapableSDKs(), aiprovider.SDKOpenCode); err != nil {
			t.Fatalf("move (pass %d): %v", i, err)
		}
	}

	reloaded, err := app.FindRecordById(aiprovider.CollectionName, record.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetString("sdk"); got != aiprovider.SDKOpenCode {
		t.Fatalf("sdk = %q, want %q", got, aiprovider.SDKOpenCode)
	}
}

// The down half puts the rows back before narrowing the field, or their next
// save would fail the collection's own select validation.
func TestTheOpenCodeMigrationReversesInTheRightOrder(t *testing.T) {
	app := bootMigratedApp(t)
	record := saveProvider(t, app, aiprovider.SDKOpenCode, "Zen", "https://opencode.ai/zen/go/v1")

	if err := moveOpenCodeRows(app, []string{aiprovider.SDKOpenCode}, aiprovider.SDKOpenAI); err != nil {
		t.Fatalf("move back: %v", err)
	}
	if err := setProviderSDKValues(app, sdksWithoutOpenCode()); err != nil {
		t.Fatalf("narrow: %v", err)
	}

	reloaded, err := app.FindRecordById(aiprovider.CollectionName, record.Id)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.GetString("sdk"); got != aiprovider.SDKOpenAI {
		t.Fatalf("sdk = %q, want %q", got, aiprovider.SDKOpenAI)
	}
	// And it still saves, which is the whole reason for the order.
	if err := app.Save(reloaded); err != nil {
		t.Fatalf("save after narrowing: %v", err)
	}
}
