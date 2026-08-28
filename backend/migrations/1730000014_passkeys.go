package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"lemmary/backend/internal/passkey"
)

// passkey_credentials stores WebAuthn credentials so an account can sign in with
// a passkey instead of a password.
//
// A separate collection rather than a JSON column on users, for three reasons.
// One passkey per account would be a trap: a passkey lives on a device, so the
// account holder who replaces their phone needs a second one enrolled before the
// first goes away, and a column would have to grow into a list anyway.
// Discoverable ("usernameless") login looks an account up by credential ID on
// every attempt, and that lookup wants a unique index, which a JSON blob cannot
// give. And a relation with CascadeDelete means deleting a user takes their
// credentials with it, matching what 1730000009 already established for the rest
// of the user-owned data.
//
// The schema itself lives in internal/passkey.EnsureCollection so this migration
// and a fresh boot cannot drift apart — the same delegation 1730000007 uses for
// ai_providers. Note in particular that the collection gets no API rules, which
// leaves them nil and keeps the whole collection off /api/collections: a session
// that could PATCH its own credential record could rewrite the sign counter or
// the public key, which are the two fields the entire scheme rests on. Every
// legitimate access goes through /api/app/passkeys.
func init() {
	m.Register(func(app core.App) error {
		_, err := passkey.EnsureCollection(app)
		return err
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(passkey.CollectionName)
		if err != nil {
			return nil
		}
		return app.Delete(collection)
	})
}
