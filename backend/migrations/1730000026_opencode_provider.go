package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// Widens ai_providers.sdk to include opencode, and moves the rows that already
// reach OpenCode Go onto it.
//
// The widening is the same reasoning as 1730000024 and 1730000025:
// EnsureCollection builds the select field from ValidSDKs but returns an
// existing collection untouched, which is every install past its first boot.
//
// The rows are the part specific to this SDK. Before it existed the only way to
// reach OpenCode was an openai or openrouter row with base_url pointing at it,
// and that is what .env.example shipped, so most installs have one. The header
// OpenCode requires used to be sent to any host under opencode.ai; it is now
// sent because the SDK says so, and a row left behind would stop carrying it --
// which is what gets a request refused outright. Its models would also lose
// their endpoint routing, and the /messages ones stop working entirely.
func init() {
	m.Register(func(app core.App) error {
		// Values before rows: a row cannot be saved with an sdk the select
		// field does not list yet.
		if err := setProviderSDKValues(app, aiprovider.ValidSDKs); err != nil {
			return err
		}
		return moveOpenCodeRows(app, openCodeCapableSDKs(), aiprovider.SDKOpenCode)
	}, func(app core.App) error {
		// Rows before values, as in 1730000024: a record whose sdk is no longer
		// in Values fails validation on its next save. Back to openai, which is
		// the SDK .env.example named and the one every such row had.
		if err := moveOpenCodeRows(app, []string{aiprovider.SDKOpenCode}, aiprovider.SDKOpenAI); err != nil {
			return err
		}
		return setProviderSDKValues(app, sdksWithoutOpenCode())
	})
}

// openCodeCapableSDKs are the SDKs a row could have used to reach OpenCode: an
// OpenAI-compatible client pointed at a base URL. Mistral is not one -- its
// provider takes a different request shape -- and neither are the keyless
// sidecars.
func openCodeCapableSDKs() []string {
	return []string{aiprovider.SDKOpenAI, aiprovider.SDKOpenRouter}
}

// moveOpenCodeRows rewrites the sdk of every provider row in from whose
// base_url addresses OpenCode.
//
// Best-effort per row: one row that will not save is not a reason to strand the
// install between two migrations, and the alias and credential are untouched
// either way.
func moveOpenCodeRows(app core.App, from []string, to string) error {
	for _, sdk := range from {
		records, err := app.FindAllRecords(aiprovider.CollectionName, dbx.HashExp{"sdk": sdk})
		if err != nil {
			continue
		}
		for _, record := range records {
			if !aiprovider.IsOpenCodeURL(record.GetString("base_url")) {
				continue
			}
			record.Set("sdk", to)
			_ = app.Save(record)
		}
	}
	return nil
}

func sdksWithoutOpenCode() []string {
	out := make([]string, 0, len(aiprovider.ValidSDKs))
	for _, sdk := range aiprovider.ValidSDKs {
		if sdk != aiprovider.SDKOpenCode {
			out = append(out, sdk)
		}
	}
	return out
}
