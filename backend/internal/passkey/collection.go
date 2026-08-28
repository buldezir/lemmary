// Package passkey implements WebAuthn (passkey) registration and login on top of
// the PocketBase users collection.
//
// PocketBase has no WebAuthn support of its own, so everything here is additive:
// credentials live in their own collection, the ceremonies run through
// go-webauthn, and a successful login is handed back to PocketBase via
// apis.RecordAuthResponse so the resulting session is an ordinary users token.
// Nothing downstream — collection rules, the paperless-ngx surface, the SPA's
// auth store — has to know a passkey was involved.
package passkey

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// CollectionName holds one WebAuthn credential per record.
const CollectionName = "passkey_credentials"

// UsersCollectionName is the auth collection passkeys belong to. Superuser
// sessions enroll against their paired users record instead; see the callers.
const UsersCollectionName = "users"

// EnsureCollection creates the credentials collection if it is missing and
// returns it either way, so the migration and a fresh boot share one definition.
//
// No API rules are set, which leaves them nil: PocketBase then serves the
// collection to superusers only, and /api/app/passkeys is the sole access path.
// That is deliberate. A credential record is not user-editable data — letting a
// session PATCH its own sign counter or swap a public key through
// /api/collections would hand an attacker the two fields the whole scheme rests
// on.
func EnsureCollection(app core.App) (*core.Collection, error) {
	if collection, err := app.FindCollectionByNameOrId(CollectionName); err == nil {
		return collection, nil
	}

	users, err := app.FindCollectionByNameOrId(UsersCollectionName)
	if err != nil {
		return nil, fmt.Errorf("find %s collection: %w", UsersCollectionName, err)
	}

	collection := core.NewBaseCollection(CollectionName)
	collection.Fields.Add(
		&core.RelationField{
			Name:          "user",
			Required:      true,
			MaxSelect:     1,
			CollectionId:  users.Id,
			CascadeDelete: true,
		},
		// The base64url raw credential ID. Stored as its own column rather than
		// read out of the credential JSON because discoverable login looks an
		// account up by it on every attempt, and that lookup wants an index.
		&core.TextField{Name: "credential_id", Required: true, Max: 1000},
		// The marshalled webauthn.Credential: public key, sign counter, flags,
		// transports and attestation. Hidden so it can never ride along in a
		// record serialization.
		&core.JSONField{Name: "credential", MaxSize: 20000, Hidden: true},
		&core.TextField{Name: "name", Max: 100},
		&core.DateField{Name: "last_used"},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	// Unique: a credential ID must resolve to exactly one account, or a
	// discoverable login would have to guess which one signed the assertion.
	collection.AddIndex("idx_passkey_credentials_credential_id", true, "credential_id", "")
	collection.AddIndex("idx_passkey_credentials_user", false, "user", "")

	if err := app.Save(collection); err != nil {
		return nil, fmt.Errorf("create %s collection: %w", CollectionName, err)
	}
	return collection, nil
}
