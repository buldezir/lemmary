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

// testUserRecordWithOAuth builds a record whose collection has OAuth2 enabled
// and the named providers configured.
func testUserRecordWithOAuth(passwordEnabled bool, providers ...string) *core.Record {
	record := testUserRecord(passwordEnabled)
	collection := record.Collection()
	collection.OAuth2.Enabled = true
	for _, name := range providers {
		collection.OAuth2.Providers = append(collection.OAuth2.Providers, core.OAuth2ProviderConfig{
			Name:         name,
			ClientId:     name + "-client",
			ClientSecret: name + "-secret",
		})
	}
	return record
}

// Built directly rather than via core.NewExternalAuth, which needs a live app.
func testExternalAuth(provider string) *core.ExternalAuth {
	collection := core.NewBaseCollection(core.CollectionNameExternalAuths)
	collection.Fields.Add(&core.TextField{Name: "provider"})
	record := core.NewRecord(collection)
	record.Set("provider", provider)
	return &core.ExternalAuth{Record: record}
}

func TestIsLastSignInMethodFalseWhilePasswordAuthIsOn(t *testing.T) {
	t.Parallel()
	// The default install: deleting every passkey stays allowed, or the guard
	// blocks the ordinary "I replaced my phone" cleanup.
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
	// Password off and no OAuth2 identity linked: the configuration where
	// removing the final passkey locks the account out.
	last, err := isLastSignInMethod(stubExternalAuths{}, testUserRecord(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !last {
		t.Fatal("with no password and no OAuth2, passkeys are the last way in")
	}
}

func TestIsLastSignInMethodFalseWhenAConfiguredOAuthProviderIsLinked(t *testing.T) {
	t.Parallel()
	finder := stubExternalAuths{auths: []*core.ExternalAuth{testExternalAuth("google")}}
	last, err := isLastSignInMethod(finder, testUserRecordWithOAuth(false, "google"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if last {
		t.Fatal("a linked OAuth2 provider that is still configured is a way in")
	}
}

func TestIsLastSignInMethodIgnoresStaleExternalAuthRows(t *testing.T) {
	t.Parallel()
	// An _externalAuths row outlives both turning OAuth2 off and removing that
	// provider, and PocketBase refuses the sign-in either way, so counting one as
	// a working method is how an account loses its last way in.
	cases := []struct {
		name   string
		record *core.Record
	}{
		{
			name:   "oauth2 disabled entirely",
			record: testUserRecord(false),
		},
		{
			name:   "provider no longer configured",
			record: testUserRecordWithOAuth(false, "github"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			finder := stubExternalAuths{auths: []*core.ExternalAuth{testExternalAuth("google")}}
			last, err := isLastSignInMethod(finder, tc.record)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !last {
				t.Fatal("a stale external-auth row is not a way in")
			}
		})
	}
}

func TestIsLastSignInMethodPropagatesLookupFailure(t *testing.T) {
	t.Parallel()
	// A failed lookup must not read as "no OAuth2 linked", which would let the
	// guard pass and delete the last credential. OAuth2 has to be enabled, since
	// that is the only path consulting the finder.
	finder := stubExternalAuths{err: errors.New("boom")}
	if _, err := isLastSignInMethod(finder, testUserRecordWithOAuth(false, "google")); err == nil {
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
