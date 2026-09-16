package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"lemmary/backend/internal/passkey"
)

// passkey_credentials stores WebAuthn credentials so an account can sign in with
// a passkey instead of a password.
//
// A separate collection rather than a JSON column on users: an account needs
// several (a passkey lives on a device, and a replacement has to be enrolled
// before the old one goes), discoverable login looks an account up by credential
// ID on every attempt and that lookup wants a unique index a JSON blob cannot
// give, and CascadeDelete matches what 1730000009 established.
//
// The schema itself lives in internal/passkey.EnsureCollection so this migration
// and a fresh boot cannot drift apart. The collection gets no API rules, which
// keeps it off /api/collections: a session that could PATCH its own credential
// record could rewrite the sign counter or the public key.
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
