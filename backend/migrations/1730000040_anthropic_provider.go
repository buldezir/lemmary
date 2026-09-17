package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// Widens ai_providers.sdk to include anthropic, which reaches api.anthropic.com
// directly rather than through the OpenCode gateway.
//
// The same reasoning as 1730000024, 1730000025, 1730000026 and 1730000035:
// EnsureCollection builds the select field from ValidSDKs but returns an
// existing collection untouched, so on any instance past its first boot saving
// an anthropic row would fail PocketBase's own select validation.
//
// No new binding column: anthropic fills the general, extraction, chat, search
// and OCR pairs that already exist. Not embeddings -- that host serves none.
func init() {
	m.Register(func(app core.App) error {
		return setProviderSDKValues(app, aiprovider.ValidSDKs)
	}, func(app core.App) error {
		// Down narrows the field, so any anthropic row left behind would fail
		// validation on its next save. Nothing deletes it here for the same
		// reason 1730000026 does not: a row holds a key the operator pasted.
		return setProviderSDKValues(app, sdksWithoutAnthropic())
	})
}

func sdksWithoutAnthropic() []string {
	out := make([]string, 0, len(aiprovider.ValidSDKs))
	for _, sdk := range aiprovider.ValidSDKs {
		if sdk != aiprovider.SDKAnthropic {
			out = append(out, sdk)
		}
	}
	return out
}
