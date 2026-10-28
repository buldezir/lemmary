package migrations

import (
	"slices"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// Widens ai_providers.sdk to include lemmary_local_ai, for the same reason as
// 1730000040: EnsureCollection returns an existing collection untouched, so on
// any instance past its first boot the row would fail select validation.
//
// No new binding column: it fills the extraction, chat, search, OCR and
// embedding pairs that already exist.
func init() {
	m.Register(func(app core.App) error {
		return setProviderSDKValues(app, aiprovider.ValidSDKs)
	}, func(app core.App) error {
		return setProviderSDKValues(app, slices.DeleteFunc(slices.Clone(aiprovider.ValidSDKs), func(sdk string) bool {
			return sdk == aiprovider.SDKLemmaryLocalAI
		}))
	})
}
