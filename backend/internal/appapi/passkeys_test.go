package appapi

import (
	"errors"
	"testing"

	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/pocketbase/pocketbase/core"
)

type stubExternalAuths struct {
	auths []*core.ExternalAuth
	err   error
}

func (s stubExternalAuths) FindAllExternalAuthsByRecord(*core.Record) ([]*core.ExternalAuth, error) {
	return s.auths, s.err
}

func testUserRecord(passwordEnabled bool) *core.Record {
	collection := core.NewAuthCollection("users")
	collection.PasswordAuth.Enabled = passwordEnabled
	record := core.NewRecord(collection)
	record.Id = "user1234567890"
	return record
}

func TestIsLastSignInMethodFalseWhilePasswordAuthIsOn(t *testing.T) {
	t.Parallel()
	// The default install. Deleting every passkey has to stay allowed here, or
	// the guard would block the ordinary "I replaced my phone" cleanup.
	last, err := isLastSignInMethod(stubExternalAuths{}, testUserRecord(true))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if last {
		t.Fatal("password auth is enabled, so passkeys are not the last way in")
	}
}

func TestIsLastSignInMethodTrueWhenOnlyPasskeysRemain(t *testing.T) {
	t.Parallel()
	// Identity/password turned off and no OAuth2 identity linked: this is the
	// configuration where removing the final passkey locks the account out.
	last, err := isLastSignInMethod(stubExternalAuths{}, testUserRecord(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !last {
		t.Fatal("with no password and no OAuth2, passkeys are the last way in")
	}
}

func TestIsLastSignInMethodFalseWhenOAuthIsLinked(t *testing.T) {
	t.Parallel()
	finder := stubExternalAuths{auths: []*core.ExternalAuth{{}}}
	last, err := isLastSignInMethod(finder, testUserRecord(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if last {
		t.Fatal("a linked OAuth2 provider is still a way in")
	}
}

func TestIsLastSignInMethodPropagatesLookupFailure(t *testing.T) {
	t.Parallel()
	// A failed lookup must not be read as "no OAuth2 linked", which would let the
	// guard pass and delete the account's last credential.
	finder := stubExternalAuths{err: errors.New("boom")}
	if _, err := isLastSignInMethod(finder, testUserRecord(false)); err == nil {
		t.Fatal("expected the lookup error to propagate")
	}
}

func TestExclusionListIsCapped(t *testing.T) {
	t.Parallel()
	credentials := make([]webauthn.Credential, maxExclusions+7)
	for i := range credentials {
		credentials[i].ID = []byte{byte(i)}
	}
	got := exclusionList(credentials)
	if len(got) != maxExclusions {
		t.Fatalf("len = %d, want %d", len(got), maxExclusions)
	}
	// Newest first: List orders by created DESC, so the head of the slice is
	// what an account holder is most likely to still be carrying.
	if len(got[0].CredentialID) != 1 || got[0].CredentialID[0] != 0 {
		t.Fatalf("first descriptor = %v, want the newest credential", got[0].CredentialID)
	}
}

func TestExclusionListPassesShortListsThrough(t *testing.T) {
	t.Parallel()
	credentials := []webauthn.Credential{{ID: []byte{1}}, {ID: []byte{2}}}
	if got := exclusionList(credentials); len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got := exclusionList(nil); len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
