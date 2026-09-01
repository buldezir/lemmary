package crypt

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

// wrapPrefix marks a wrapped key.
const wrapPrefix = "lmwrap1:"

// WrapKey seals a master key under a key-encryption key.
//
// XChaCha20-Poly1305 is used rather than AES-GCM because its 24-byte random
// nonce is collision-safe without any counter state to keep. There is nowhere
// convenient to persist nonce counters here, and a silent nonce reuse under
// AES-GCM would be catastrophic rather than merely wrong.
//
// aad binds the wrap to its slot and to the KDF parameters it was written with,
// so a wrap cannot be moved between slots and its cost parameters cannot be
// rewritten downward by an attacker who can edit the keyring file.
func WrapKey(kek Key, mk Key, aad string) (string, error) {
	aead, err := chacha20poly1305.NewX(kek[:])
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := aead.Seal(nonce, nonce, mk[:], []byte(aad))
	return wrapPrefix + base64.RawURLEncoding.EncodeToString(sealed), nil
}

// UnwrapKey recovers a master key sealed by WrapKey.
//
// A wrong credential is indistinguishable from a tampered wrap: both surface as
// ErrCorrupt. That is deliberate — the AEAD tag is the credential check, so
// nothing separate is stored that could tell an attacker which of the two they
// achieved.
func UnwrapKey(kek Key, wrapped string, aad string) (Key, error) {
	wrapped = strings.TrimSpace(wrapped)
	if !strings.HasPrefix(wrapped, wrapPrefix) {
		return Key{}, ErrNotSealed
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(wrapped, wrapPrefix))
	if err != nil {
		return Key{}, ErrCorrupt
	}
	aead, err := chacha20poly1305.NewX(kek[:])
	if err != nil {
		return Key{}, err
	}
	if len(raw) < aead.NonceSize() {
		return Key{}, ErrCorrupt
	}
	nonce, ct := raw[:aead.NonceSize()], raw[aead.NonceSize():]
	plain, err := aead.Open(nil, nonce, ct, []byte(aad))
	if err != nil {
		return Key{}, ErrCorrupt
	}
	defer clear(plain)
	if len(plain) != KeyLen {
		return Key{}, ErrCorrupt
	}
	var mk Key
	copy(mk[:], plain)
	return mk, nil
}

// IsWrappedKey reports whether s looks like a wrapped key.
func IsWrappedKey(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), wrapPrefix)
}

// KeyID returns a short, non-secret identifier for a key.
//
// It exists so logs and errors can say *which* key was involved without ever
// printing key material, and so two wraps can be checked for holding the same
// underlying key.
func KeyID(k Key) string {
	sub, err := subkey(k, nil, infoKeyID)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sub[:8])
}

// PasskeyKEK derives a key-encryption key from a WebAuthn PRF secret.
//
// Like a recovery code, the PRF output is already uniform high-entropy material,
// so password stretching would add latency and no security. HKDF is still worth
// doing: it gives domain separation, so the value this package uses as a key is
// never the same bytes the authenticator hands out and might hand to something
// else.
func PasskeyKEK(prf []byte) (Key, error) {
	if len(prf) != KeyLen {
		return Key{}, fmt.Errorf("crypt: prf secret is %d bytes, want %d", len(prf), KeyLen)
	}
	var raw Key
	copy(raw[:], prf)
	defer raw.Zero()
	return subkey(raw, nil, "lemmary/passkey/v1")
}
