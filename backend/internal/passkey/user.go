package passkey

import (
	"strings"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pocketbase/pocketbase/core"
)

// Account adapts a PocketBase users record to the webauthn.User interface.
type Account struct {
	record      *core.Record
	credentials []webauthn.Credential
}

// NewAccount loads the account's credentials and wraps the record.
func NewAccount(app core.App, record *core.Record) (*Account, error) {
	credentials, err := Credentials(app, record.Id)
	if err != nil {
		return nil, err
	}
	return &Account{record: record, credentials: credentials}, nil
}

// Record exposes the underlying users record so a caller can mint a session for it.
func (a *Account) Record() *core.Record {
	return a.record
}

// WebAuthnID is the user handle the authenticator stores alongside the
// credential. The PocketBase record id is used because it is stable for the life
// of the account, opaque, and not personal data — an email here would be burned
// into the authenticator and would go stale the moment the address changed.
func (a *Account) WebAuthnID() []byte {
	return []byte(a.record.Id)
}

// WebAuthnName is the account identifier shown in the authenticator's own
// account picker, which is why it is the email rather than the display name.
func (a *Account) WebAuthnName() string {
	if email := strings.TrimSpace(a.record.Email()); email != "" {
		return email
	}
	return a.record.Id
}

// WebAuthnDisplayName is the human-palatable name for the same picker.
func (a *Account) WebAuthnDisplayName() string {
	if name := strings.TrimSpace(a.record.GetString("name")); name != "" {
		return name
	}
	return a.WebAuthnName()
}

// WebAuthnCredentials returns the credentials already enrolled for the account.
func (a *Account) WebAuthnCredentials() []webauthn.Credential {
	return a.credentials
}
