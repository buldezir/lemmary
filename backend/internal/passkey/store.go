package passkey

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// ErrNotFound is returned when no credential record matches the lookup. It is
// deliberately the same error for "no such record" and "belongs to someone
// else", so a caller cannot use the distinction to probe for other accounts'
// credential ids.
var ErrNotFound = errors.New("passkey not found")

// EncodeCredentialID renders a raw credential ID the way the browser and the
// WebAuthn JSON encoding do: base64url, unpadded.
func EncodeCredentialID(raw []byte) string {
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Info is the client-facing view of a credential. The public key, sign counter
// and attestation never leave the server.
type Info struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Created  string `json:"created"`
	LastUsed string `json:"last_used"`
}

// ToInfo projects a credential record for the API.
func ToInfo(record *core.Record) Info {
	lastUsed := ""
	if value := record.GetDateTime("last_used"); !value.IsZero() {
		lastUsed = value.String()
	}
	return Info{
		ID:       record.Id,
		Name:     record.GetString("name"),
		Created:  record.GetDateTime("created").String(),
		LastUsed: lastUsed,
	}
}

// List returns the account's credential records, newest first.
func List(app core.App, userID string) ([]*core.Record, error) {
	records := []*core.Record{}
	err := app.RecordQuery(CollectionName).
		AndWhere(dbx.HashExp{"user": userID}).
		OrderBy("created DESC").
		All(&records)
	if err != nil {
		return nil, err
	}
	return records, nil
}

// Credentials decodes the account's credentials for go-webauthn. A record whose
// JSON fails to decode is skipped rather than failing the whole ceremony: one
// corrupt row should not lock an account out of the passkeys that still work.
func Credentials(app core.App, userID string) ([]webauthn.Credential, error) {
	records, err := List(app, userID)
	if err != nil {
		return nil, err
	}
	credentials := make([]webauthn.Credential, 0, len(records))
	for _, record := range records {
		credential, err := DecodeCredential(record)
		if err != nil {
			app.Logger().Warn("skipping undecodable passkey credential",
				"record", record.Id, "error", err)
			continue
		}
		credentials = append(credentials, *credential)
	}
	return credentials, nil
}

// FindByCredentialID resolves the record for a raw credential ID as presented in
// an assertion.
func FindByCredentialID(app core.App, rawID []byte) (*core.Record, error) {
	records := []*core.Record{}
	err := app.RecordQuery(CollectionName).
		AndWhere(dbx.HashExp{"credential_id": EncodeCredentialID(rawID)}).
		Limit(1).
		All(&records)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrNotFound
	}
	return records[0], nil
}

// FindOwned resolves one of the account's own credentials. Scoping the query by
// user is what keeps an authenticated session from touching another account's
// credential by guessing a record id.
func FindOwned(app core.App, userID, recordID string) (*core.Record, error) {
	records := []*core.Record{}
	err := app.RecordQuery(CollectionName).
		AndWhere(dbx.HashExp{"id": recordID, "user": userID}).
		Limit(1).
		All(&records)
	if err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return nil, ErrNotFound
	}
	return records[0], nil
}

// Count reports how many passkeys the account has.
func Count(app core.App, userID string) (int, error) {
	total, err := app.CountRecords(CollectionName, dbx.HashExp{"user": userID})
	return int(total), err
}

// Create stores a freshly registered credential.
func Create(app core.App, userID, name string, credential *webauthn.Credential) (*core.Record, error) {
	collection, err := app.FindCollectionByNameOrId(CollectionName)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(credential)
	if err != nil {
		return nil, fmt.Errorf("encode credential: %w", err)
	}

	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("credential_id", EncodeCredentialID(credential.ID))
	record.Set("credential", types.JSONRaw(encoded))
	record.Set("name", NormalizeName(name))
	if err := app.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

// TouchCredential writes back the credential state a successful assertion
// updated — the sign counter above all, which is the replay defence — and stamps
// last_used. Skipping this is the classic WebAuthn storage bug: the counter would
// never advance and a cloned authenticator would go unnoticed.
func TouchCredential(app core.App, record *core.Record, credential *webauthn.Credential) error {
	encoded, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("encode credential: %w", err)
	}
	record.Set("credential", types.JSONRaw(encoded))
	record.Set("last_used", types.NowDateTime())
	return app.Save(record)
}

// Rename sets the user-visible label.
func Rename(app core.App, record *core.Record, name string) error {
	record.Set("name", NormalizeName(name))
	return app.Save(record)
}

// NormalizeName trims a label and falls back to something recognizable, so the
// management list never shows a blank row.
func NormalizeName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "Passkey"
	}
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	return name
}

// DecodeCredential reads the stored webauthn.Credential back out of a record.
func DecodeCredential(record *core.Record) (*webauthn.Credential, error) {
	raw := record.GetString("credential")
	if raw == "" {
		return nil, errors.New("empty credential payload")
	}
	credential := &webauthn.Credential{}
	if err := json.Unmarshal([]byte(raw), credential); err != nil {
		return nil, err
	}
	if len(credential.ID) == 0 {
		return nil, errors.New("credential payload has no id")
	}
	return credential, nil
}
