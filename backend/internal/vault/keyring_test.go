package vault

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"lemmary/backend/internal/crypt"
)

// newTestKeyring keeps Argon2 at the lowest cost Validate accepts so the suite
// stays fast; production defaults are exercised in the crypt package.
func newTestKeyring(t *testing.T, userID, password string) (*Keyring, crypt.Key, string) {
	t.Helper()
	kr, mk, code, err := NewKeyring(userID, password)
	if err != nil {
		t.Fatalf("NewKeyring: %v", err)
	}
	return kr, mk, code
}

func cheapen(t *testing.T, kr *Keyring, mk crypt.Key, userID, password string) {
	t.Helper()
	params, err := crypt.NewKDFParams()
	if err != nil {
		t.Fatalf("NewKDFParams: %v", err)
	}
	params.MemKiB, params.Time, params.Lanes = 16*1024, 2, 2
	kek, err := crypt.DeriveKEK(password, params)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	w := Wrap{ID: wrapID(userID, "pw"), User: userID, Type: WrapPassword, KDF: &params}
	if err := kr.sealInto(&w, kek, mk); err != nil {
		t.Fatalf("sealInto: %v", err)
	}
}

func TestKeyringUnlocksWithPasswordAndRecoveryCode(t *testing.T) {
	kr, mk, code := newTestKeyring(t, "user1", "hunter2")

	got, id, err := kr.Unlock(Credential{Password: "hunter2"})
	if err != nil {
		t.Fatalf("unlock with password: %v", err)
	}
	if got != mk {
		t.Fatal("password unlock returned a different master key")
	}
	if id != "u_user1:pw" {
		t.Fatalf("opened wrap %q, want the password wrap", id)
	}

	got, id, err = kr.Unlock(Credential{RecoveryCode: code})
	if err != nil {
		t.Fatalf("unlock with recovery code: %v", err)
	}
	if got != mk {
		t.Fatal("recovery unlock returned a different master key")
	}
	if id == "u_user1:pw" {
		t.Fatal("recovery credential opened the password wrap")
	}
}

// The property that makes instance-wide encryption work: a second user's own
// credential opens the same master key, so whoever signs in first unlocks it.
func TestAnyUsersCredentialUnlocksTheSameMasterKey(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "first-pass")
	cheapen(t, kr, mk, "user1", "first-pass")
	if err := kr.AddPassword(mk, "user2", "second-pass"); err != nil {
		t.Fatalf("AddPassword: %v", err)
	}
	cheapen(t, kr, mk, "user2", "second-pass")

	for _, pw := range []string{"first-pass", "second-pass"} {
		got, _, err := kr.Unlock(Credential{Password: pw})
		if err != nil {
			t.Fatalf("unlock with %q: %v", pw, err)
		}
		if got != mk {
			t.Fatalf("unlock with %q returned a different master key", pw)
		}
	}
}

func TestKeyringRejectsWrongCredential(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "right")
	cheapen(t, kr, mk, "user1", "right")

	before, err := json.Marshal(kr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	for name, c := range map[string]Credential{
		"wrong password":       {Password: "wrong"},
		"empty credential":     {},
		"garbage recovery":     {RecoveryCode: "ZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZ-ZZZZ"},
		"short prf":            {PRF: []byte("too short")},
		"password as recovery": {RecoveryCode: "right"},
	} {
		if _, _, err := kr.Unlock(c); !errors.Is(err, ErrWrongKey) {
			t.Fatalf("%s: got %v, want ErrWrongKey", name, err)
		}
	}

	after, err := json.Marshal(kr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("a failed unlock mutated the keyring")
	}
}

// Rewriting the recorded Argon2 cost downward is how an attacker with write
// access to the volume would make offline guessing cheap. The wrap's own AAD
// covers those parameters, so the rewrite must fail to authenticate.
func TestKeyringRejectsArgon2ParameterDowngrade(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "hunter2")
	cheapen(t, kr, mk, "user1", "hunter2")

	if _, _, err := kr.Unlock(Credential{Password: "hunter2"}); err != nil {
		t.Fatalf("baseline unlock: %v", err)
	}

	for i := range kr.Wraps {
		if kr.Wraps[i].Type != WrapPassword {
			continue
		}
		kr.Wraps[i].KDF.MemKiB = 8 * 1024
		kr.Wraps[i].KDF.Time = 1
	}
	if _, _, err := kr.Unlock(Credential{Password: "hunter2"}); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("downgraded parameters still unlocked: %v", err)
	}
}

// Moving a wrap between slots or users must not work either.
func TestKeyringRejectsRelabelledWrap(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "hunter2")
	cheapen(t, kr, mk, "user1", "hunter2")

	for i := range kr.Wraps {
		if kr.Wraps[i].Type == WrapPassword {
			kr.Wraps[i].User = "user2"
			kr.Wraps[i].ID = wrapID("user2", "pw")
		}
	}
	if _, _, err := kr.Unlock(Credential{Password: "hunter2"}); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("relabelled wrap still unlocked: %v", err)
	}
}

// A keyring stitched together from two vaults would otherwise let new data be
// written under a key the other wraps cannot open.
func TestKeyringRejectsForeignMasterKey(t *testing.T) {
	krA, mkA, _ := newTestKeyring(t, "user1", "pass-a")
	cheapen(t, krA, mkA, "user1", "pass-a")
	krB, mkB, _ := newTestKeyring(t, "user2", "pass-b")
	cheapen(t, krB, mkB, "user2", "pass-b")

	var foreign Wrap
	for _, w := range krB.Wraps {
		if w.Type == WrapPassword {
			foreign = w
		}
	}
	krA.Wraps = append(krA.Wraps, foreign)

	_, _, err := krA.Unlock(Credential{Password: "pass-b"})
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("foreign wrap: got %v, want ErrCorrupt", err)
	}

	if err := krA.AddPassword(mkB, "user3", "pass-c"); err == nil {
		t.Fatal("adding a wrap for a foreign master key was allowed")
	}
}

func TestKeyringPasskeyWrap(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "hunter2")

	prf := make([]byte, crypt.KeyLen)
	for i := range prf {
		prf[i] = byte(i + 1)
	}
	if err := kr.AddPasskey(mk, "user1", "credential-abc", prf); err != nil {
		t.Fatalf("AddPasskey: %v", err)
	}

	got, _, err := kr.Unlock(Credential{PRF: prf, CredID: "credential-abc"})
	if err != nil {
		t.Fatalf("unlock with prf: %v", err)
	}
	if got != mk {
		t.Fatal("passkey unlock returned a different master key")
	}

	wrong := make([]byte, crypt.KeyLen)
	if _, _, err := kr.Unlock(Credential{PRF: wrong}); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("wrong prf: got %v, want ErrWrongKey", err)
	}
	if err := kr.AddPasskey(mk, "user1", "c", []byte("short")); err == nil {
		t.Fatal("accepted an undersized prf secret")
	}
}

// Adding a credential must not disturb the ones already there.
func TestAddingAWrapDoesNotInvalidateOthers(t *testing.T) {
	kr, mk, code := newTestKeyring(t, "user1", "hunter2")
	cheapen(t, kr, mk, "user1", "hunter2")

	for i := 0; i < 3; i++ {
		if err := kr.AddPassword(mk, "extra", "pw"); err != nil {
			t.Fatalf("AddPassword: %v", err)
		}
		cheapen(t, kr, mk, "extra", "pw")
	}

	if _, _, err := kr.Unlock(Credential{Password: "hunter2"}); err != nil {
		t.Fatalf("original password stopped working: %v", err)
	}
	if _, _, err := kr.Unlock(Credential{RecoveryCode: code}); err != nil {
		t.Fatalf("original recovery code stopped working: %v", err)
	}
}

// Re-adding a password for the same user replaces rather than accumulates, so a
// password change does not leave the old one usable.
func TestPasswordChangeReplacesTheOldWrap(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "old")
	cheapen(t, kr, mk, "user1", "old")
	before := len(kr.Wraps)

	if err := kr.AddPassword(mk, "user1", "new"); err != nil {
		t.Fatalf("AddPassword: %v", err)
	}
	cheapen(t, kr, mk, "user1", "new")

	if len(kr.Wraps) != before {
		t.Fatalf("wrap count went %d -> %d; the old wrap was not replaced", before, len(kr.Wraps))
	}
	if _, _, err := kr.Unlock(Credential{Password: "old"}); !errors.Is(err, ErrWrongKey) {
		t.Fatal("the old password still unlocks")
	}
	if _, _, err := kr.Unlock(Credential{Password: "new"}); err != nil {
		t.Fatalf("the new password does not unlock: %v", err)
	}
}

func TestRemoveWrapsRefusesToLockEveryoneOut(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "hunter2")
	cheapen(t, kr, mk, "user1", "hunter2")

	if !kr.HasWrapForUser("user1") {
		t.Fatal("HasWrapForUser missed the password wrap")
	}
	// The recovery wrap has no user, so removing user1 leaves a way in.
	if err := kr.RemoveWrapsForUser("user1"); err != nil {
		t.Fatalf("RemoveWrapsForUser: %v", err)
	}
	if kr.HasWrapForUser("user1") {
		t.Fatal("wrap survived removal")
	}

	// Now only the recovery wrap remains; dropping it must be refused.
	kr.Wraps[0].User = "user1"
	if err := kr.RemoveWrapsForUser("user1"); !errors.Is(err, ErrLastWrap) {
		t.Fatalf("removing the last wrap: got %v, want ErrLastWrap", err)
	}
	if len(kr.Wraps) == 0 {
		t.Fatal("the last wrap was removed anyway")
	}
}

func TestKeyringSaveLoadRoundTripAndValidation(t *testing.T) {
	dir := t.TempDir()
	kr, mk, _ := newTestKeyring(t, "user1", "hunter2")
	cheapen(t, kr, mk, "user1", "hunter2")

	if err := kr.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, keyringName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("keyring mode is %v, want 0600", fi.Mode().Perm())
	}

	loaded, err := LoadKeyring(dir)
	if err != nil {
		t.Fatalf("LoadKeyring: %v", err)
	}
	got, _, err := loaded.Unlock(Credential{Password: "hunter2"})
	if err != nil {
		t.Fatalf("unlock after reload: %v", err)
	}
	if got != mk {
		t.Fatal("reloaded keyring yields a different master key")
	}

	if _, err := LoadKeyring(t.TempDir()); !errors.Is(err, ErrNoKeyring) {
		t.Fatalf("missing keyring: got %v, want ErrNoKeyring", err)
	}

	for name, body := range map[string]string{
		"not json":    "{",
		"bad version": `{"v":99,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","wraps":[{"id":"a"}]}`,
		"short salt":  `{"v":1,"salt":"AA==","wraps":[{"id":"a"}]}`,
		"no wraps":    `{"v":1,"salt":"AAAAAAAAAAAAAAAAAAAAAA==","wraps":[]}`,
	} {
		bad := t.TempDir()
		if err := os.WriteFile(filepath.Join(bad, keyringName), []byte(body), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
		if _, err := LoadKeyring(bad); err == nil {
			t.Fatalf("%s: LoadKeyring accepted it", name)
		}
	}
}

// The keyring sits on the untrusted volume, so it must not carry a customer's
// user list or any key material.
func TestKeyringFileLeaksNoSecrets(t *testing.T) {
	dir := t.TempDir()
	kr, mk, code := newTestKeyring(t, "user1", "sup3rs3cret")
	cheapen(t, kr, mk, "user1", "sup3rs3cret")
	if err := kr.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	b, err := os.ReadFile(filepath.Join(dir, keyringName))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	body := string(b)
	for _, secret := range []string{"sup3rs3cret", code, string(mk[:])} {
		if secret != "" && contains(body, secret) {
			t.Fatalf("keyring file contains a secret")
		}
	}
	if contains(body, "@") {
		t.Fatal("keyring file appears to contain an email address")
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}

func TestSubkeysAreDistinctAndSaltBound(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "hunter2")

	blob, manifest, name, err := kr.Subkeys(mk)
	if err != nil {
		t.Fatalf("Subkeys: %v", err)
	}
	if blob == manifest || blob == name || manifest == name {
		t.Fatal("subkeys collide")
	}
	if blob == mk || manifest == mk || name == mk {
		t.Fatal("a subkey is the master key itself")
	}

	other := *kr
	other.Salt = append([]byte(nil), kr.Salt...)
	other.Salt[0] ^= 0xFF
	blob2, _, _, err := other.Subkeys(mk)
	if err != nil {
		t.Fatalf("Subkeys: %v", err)
	}
	if blob == blob2 {
		t.Fatal("subkey derivation ignores the salt")
	}
}

// The defect this closes: a vault created before any account exists keeps the
// provisioning password as a valid key to the archive forever, so a user who
// changes their password has revoked nothing.
func TestBootstrapWrapIsRevokedOnceAUserIsEnrolled(t *testing.T) {
	kr, mk, code := newTestKeyring(t, "", "provisioning-password")
	cheapen(t, kr, mk, "", "provisioning-password")

	// Before enrolment it is one of only two ways in, so it must survive.
	if err := kr.RemoveBootstrapWrap(); !errors.Is(err, ErrLastWrap) {
		t.Fatalf("removing the bootstrap wrap with no user enrolled: got %v, want ErrLastWrap", err)
	}
	if _, _, err := kr.Unlock(Credential{Password: "provisioning-password"}); err != nil {
		t.Fatalf("the bootstrap credential stopped working: %v", err)
	}

	cheapen(t, kr, mk, "user1", "hunter2")
	if err := kr.RemoveBootstrapWrap(); err != nil {
		t.Fatalf("RemoveBootstrapWrap: %v", err)
	}

	if _, _, err := kr.Unlock(Credential{Password: "provisioning-password"}); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("the provisioning password still opens the archive: %v", err)
	}
	// The credentials that replaced it must be untouched — a wrap's AAD covers
	// only itself, but a bug in the filter would drop the wrong rows.
	if _, _, err := kr.Unlock(Credential{Password: "hunter2"}); err != nil {
		t.Fatalf("the user credential stopped working: %v", err)
	}
	if _, _, err := kr.Unlock(Credential{RecoveryCode: code}); err != nil {
		t.Fatalf("the recovery code stopped working: %v", err)
	}
}

// Calling it on a keyring that never had one must be a no-op, not a refusal:
// enroll runs it on every password change for the life of the instance.
func TestRemoveBootstrapWrapIsANoOpWhenThereIsNone(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "user1", "hunter2")
	cheapen(t, kr, mk, "user1", "hunter2")

	before := len(kr.Wraps)
	if err := kr.RemoveBootstrapWrap(); err != nil {
		t.Fatalf("RemoveBootstrapWrap on a keyring without one: %v", err)
	}
	if len(kr.Wraps) != before {
		t.Fatalf("wraps went from %d to %d", before, len(kr.Wraps))
	}
	if _, _, err := kr.Unlock(Credential{Password: "hunter2"}); err != nil {
		t.Fatalf("the user credential stopped working: %v", err)
	}
}

// The recovery code alone is not enough to justify the removal. It is printed
// once by `vault init` and whoever ran that may not have kept it, so dropping
// the bootstrap wrap while it is the only survivor can strand the archive.
func TestRemoveBootstrapWrapRefusesWhenOnlyTheRecoveryCodeSurvives(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "", "provisioning-password")
	cheapen(t, kr, mk, "", "provisioning-password")

	for _, w := range kr.Wraps {
		if w.Type == WrapRecovery && w.User != "" {
			t.Fatal("the recovery wrap should have no user")
		}
	}

	if err := kr.RemoveBootstrapWrap(); !errors.Is(err, ErrLastWrap) {
		t.Fatalf("got %v, want ErrLastWrap", err)
	}
	if _, _, err := kr.Unlock(Credential{Password: "provisioning-password"}); err != nil {
		t.Fatalf("the bootstrap credential was removed anyway: %v", err)
	}
}

// A passkey is a real user credential too, so enrolling one is enough.
func TestPasskeyEnrolmentAlsoAllowsRevokingTheBootstrapWrap(t *testing.T) {
	kr, mk, _ := newTestKeyring(t, "", "provisioning-password")
	cheapen(t, kr, mk, "", "provisioning-password")

	prf := make([]byte, 32)
	for i := range prf {
		prf[i] = byte(i)
	}
	if err := kr.AddPasskey(mk, "user1", "cred-1", prf); err != nil {
		t.Fatalf("AddPasskey: %v", err)
	}

	if err := kr.RemoveBootstrapWrap(); err != nil {
		t.Fatalf("RemoveBootstrapWrap: %v", err)
	}
	if _, _, err := kr.Unlock(Credential{Password: "provisioning-password"}); !errors.Is(err, ErrWrongKey) {
		t.Fatalf("the provisioning password still opens the archive: %v", err)
	}
}
