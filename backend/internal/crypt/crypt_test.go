package crypt

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// testKDF uses the lowest parameters Validate accepts, so the suite stays fast.
// Production defaults are exercised by TestDefaultKDFParamsValidate.
func testKDF(t *testing.T) KDFParams {
	t.Helper()
	p, err := NewKDFParams()
	if err != nil {
		t.Fatalf("NewKDFParams: %v", err)
	}
	p.MemKiB = 8 * 1024
	p.Time = 1
	p.Lanes = 1
	return p
}

func mustKey(t *testing.T) Key {
	t.Helper()
	k, err := NewKey()
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return k
}

func TestDefaultKDFParamsValidate(t *testing.T) {
	p, err := NewKDFParams()
	if err != nil {
		t.Fatalf("NewKDFParams: %v", err)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("default params rejected: %v", err)
	}
	if p.MemKiB != DefaultArgonMemKiB || p.Time != DefaultArgonTime || p.Lanes != DefaultArgonLanes {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	if len(p.Salt) != SaltLen {
		t.Fatalf("salt is %d bytes, want %d", len(p.Salt), SaltLen)
	}
}

func TestKDFParamsRoundTripThroughStorage(t *testing.T) {
	p := testKDF(t)
	encoded, err := p.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	back, err := DecodeKDFParams(encoded)
	if err != nil {
		t.Fatalf("DecodeKDFParams: %v", err)
	}
	if back.Algo != p.Algo || back.MemKiB != p.MemKiB || back.Time != p.Time || back.Lanes != p.Lanes {
		t.Fatalf("params changed: %+v vs %+v", back, p)
	}
	if !bytes.Equal(back.Salt, p.Salt) {
		t.Fatalf("salt changed")
	}
}

// A weakened parameter set read back from the database must be refused: an
// operator who can edit the users table could otherwise turn the KDF into a
// no-op and make offline password guessing cheap.
func TestDecodeKDFParamsRejectsWeakened(t *testing.T) {
	cases := map[string]string{
		"tiny memory":  `{"a":"argon2id","m":64,"t":3,"p":4,"s":"AAAAAAAAAAAAAAAAAAAAAA"}`,
		"zero time":    `{"a":"argon2id","m":65536,"t":0,"p":4,"s":"AAAAAAAAAAAAAAAAAAAAAA"}`,
		"zero lanes":   `{"a":"argon2id","m":65536,"t":3,"p":0,"s":"AAAAAAAAAAAAAAAAAAAAAA"}`,
		"unknown algo": `{"a":"md5","m":65536,"t":3,"p":4,"s":"AAAAAAAAAAAAAAAAAAAAAA"}`,
		"short salt":   `{"a":"argon2id","m":65536,"t":3,"p":4,"s":"AAAA"}`,
		"not json":     `nonsense`,
		"empty":        ``,
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeKDFParams(encoded); err == nil {
				t.Fatalf("expected rejection of %s", name)
			}
		})
	}
}

func TestDeriveKEKIsDeterministicAndSaltDependent(t *testing.T) {
	p := testKDF(t)
	a, err := DeriveKEK("correct horse", p)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	b, err := DeriveKEK("correct horse", p)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	if a != b {
		t.Fatal("same password and salt produced different keys")
	}

	other := testKDF(t) // fresh salt
	c, err := DeriveKEK("correct horse", other)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	if a == c {
		t.Fatal("different salts produced the same key")
	}

	d, err := DeriveKEK("wrong horse", p)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	if a == d {
		t.Fatal("different passwords produced the same key")
	}
}

func TestKeyIDIdentifiesTheUnderlyingKey(t *testing.T) {
	dek := mustKey(t)
	other := mustKey(t)
	if KeyID(dek) == "" {
		t.Fatal("KeyID returned empty")
	}
	if KeyID(dek) != KeyID(dek) {
		t.Fatal("KeyID is not stable")
	}
	if KeyID(dek) == KeyID(other) {
		t.Fatal("distinct keys share a key id")
	}
	if strings.Contains(KeyID(dek), string(dek[:8])) {
		t.Fatal("key id leaks key material")
	}
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	p := testKDF(t)
	mk := mustKey(t)

	kek, err := DeriveKEK("correct horse battery staple", p)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}

	const aad = "slot=pw|kdf=argon2id"
	wrapped, err := WrapKey(kek, mk, aad)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	if !IsWrappedKey(wrapped) {
		t.Fatalf("IsWrappedKey(%q) = false", wrapped)
	}
	if strings.Contains(wrapped, base64.RawURLEncoding.EncodeToString(mk[:])) {
		t.Fatal("wrapped blob contains the master key verbatim")
	}

	got, err := UnwrapKey(kek, wrapped, aad)
	if err != nil {
		t.Fatalf("UnwrapKey: %v", err)
	}
	if got != mk {
		t.Fatal("unwrapped key does not match the wrapped one")
	}
}

func TestWrapIsNonDeterministic(t *testing.T) {
	kek, mk := mustKey(t), mustKey(t)
	a, err := WrapKey(kek, mk, "aad")
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	b, err := WrapKey(kek, mk, "aad")
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	if a == b {
		t.Fatal("two wraps of the same key are identical; the nonce is not random")
	}
}

func TestUnwrapKeyRejectsWrongCredentialAndWrongAAD(t *testing.T) {
	p := testKDF(t)
	mk := mustKey(t)

	kek, err := DeriveKEK("right", p)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	const aad = "slot=pw|kdf=argon2id"
	wrapped, err := WrapKey(kek, mk, aad)
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}

	wrongKEK, err := DeriveKEK("wrong", p)
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	if _, err := UnwrapKey(wrongKEK, wrapped, aad); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("wrong credential: got %v, want ErrCorrupt", err)
	}

	// A rewritten AAD is how an attacker would try to downgrade the recorded
	// Argon2 cost; the tag must refuse it.
	if _, err := UnwrapKey(kek, wrapped, "slot=pw|kdf=argon2id-weak"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("tampered aad: got %v, want ErrCorrupt", err)
	}

	if _, err := UnwrapKey(kek, "not-a-wrap", aad); !errors.Is(err, ErrNotSealed) {
		t.Fatalf("non-wrap input: got %v, want ErrNotSealed", err)
	}
}

func TestUnwrapKeyRejectsTruncatedAndTamperedWraps(t *testing.T) {
	kek, mk := mustKey(t), mustKey(t)
	wrapped, err := WrapKey(kek, mk, "aad")
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(wrapped, "lmwrap1:"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	for i := range raw {
		tampered := append([]byte(nil), raw...)
		tampered[i] ^= 0x01
		blob := "lmwrap1:" + base64.RawURLEncoding.EncodeToString(tampered)
		if _, err := UnwrapKey(kek, blob, "aad"); err == nil {
			t.Fatalf("flipping byte %d of the wrap still unwrapped", i)
		}
	}

	for n := 0; n < len(raw); n++ {
		blob := "lmwrap1:" + base64.RawURLEncoding.EncodeToString(raw[:n])
		if _, err := UnwrapKey(kek, blob, "aad"); err == nil {
			t.Fatalf("truncating the wrap to %d bytes still unwrapped", n)
		}
	}
}

func TestSubkeysAreSeparatedByInfo(t *testing.T) {
	mk := mustKey(t)
	salt := []byte("salt")

	seen := map[Key]string{}
	for _, info := range []string{InfoBlob, InfoManifest, InfoBlobName, InfoVerifier} {
		k, err := Subkey(mk, salt, info)
		if err != nil {
			t.Fatalf("Subkey(%s): %v", info, err)
		}
		if k == mk {
			t.Fatalf("Subkey(%s) returned the master key itself", info)
		}
		if prev, dup := seen[k]; dup {
			t.Fatalf("Subkey(%s) collides with Subkey(%s)", info, prev)
		}
		seen[k] = info
	}

	// Same info, different salt must not collide either.
	a, _ := Subkey(mk, []byte("salt-a"), InfoBlob)
	b, _ := Subkey(mk, []byte("salt-b"), InfoBlob)
	if a == b {
		t.Fatal("subkey ignores the salt")
	}
}

func TestKeyZeroAndIsZero(t *testing.T) {
	k := mustKey(t)
	if k.IsZero() {
		t.Fatal("a fresh random key reports IsZero")
	}
	k.Zero()
	if !k.IsZero() {
		t.Fatal("Zero did not clear the key")
	}
}

func TestPasskeyKEKIsDerivedNotRaw(t *testing.T) {
	prf := make([]byte, KeyLen)
	for i := range prf {
		prf[i] = byte(i + 1)
	}

	kek, err := PasskeyKEK(prf)
	if err != nil {
		t.Fatalf("PasskeyKEK: %v", err)
	}

	// The key must not be the authenticator's secret verbatim: that secret may be
	// handed to other relying-party uses, and reusing it as our key would couple
	// them together.
	var raw Key
	copy(raw[:], prf)
	if kek == raw {
		t.Fatal("PasskeyKEK returned the PRF secret unchanged")
	}

	again, err := PasskeyKEK(prf)
	if err != nil {
		t.Fatalf("PasskeyKEK: %v", err)
	}
	if again != kek {
		t.Fatal("PasskeyKEK is not deterministic")
	}

	other := append([]byte(nil), prf...)
	other[0] ^= 0xFF
	diff, err := PasskeyKEK(other)
	if err != nil {
		t.Fatalf("PasskeyKEK: %v", err)
	}
	if diff == kek {
		t.Fatal("distinct PRF secrets derived the same key")
	}

	// And it must be separated from the recovery-code derivation.
	code, err := NewRecoveryCode()
	if err != nil {
		t.Fatalf("NewRecoveryCode: %v", err)
	}
	rc, err := RecoveryKEK(code)
	if err != nil {
		t.Fatalf("RecoveryKEK: %v", err)
	}
	if rc == kek {
		t.Fatal("passkey and recovery derivations collide")
	}

	for _, bad := range [][]byte{nil, []byte("short"), make([]byte, KeyLen+1)} {
		if _, err := PasskeyKEK(bad); err == nil {
			t.Fatalf("PasskeyKEK accepted a %d-byte secret", len(bad))
		}
	}
}
