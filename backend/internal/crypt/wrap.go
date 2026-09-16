package crypt

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/chacha20poly1305"
)

const wrapPrefix = "lmwrap1:"

// WrapKey seals a master key under a key-encryption key.
//
// XChaCha20-Poly1305 rather than AES-GCM: its 24-byte random nonce is
// collision-safe with no counter state to persist, and a silent nonce reuse
// under AES-GCM would be catastrophic. aad binds the wrap to its slot and KDF
// parameters, so an attacker editing the keyring file can neither move a wrap
// between slots nor rewrite its cost downward.
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

// UnwrapKey recovers a master key sealed by WrapKey. The AEAD tag is the
// credential check, so a wrong credential and a tampered wrap both surface as
// ErrCorrupt.
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

func IsWrappedKey(s string) bool {
	return strings.HasPrefix(strings.TrimSpace(s), wrapPrefix)
}

// KeyID is a short non-secret identifier, so logs can name a key without
// printing key material and two wraps can be checked for holding the same one.
func KeyID(k Key) string {
	sub, err := subkey(k, nil, infoKeyID)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", sub[:8])
}

// PasskeyKEK derives a KEK from a WebAuthn PRF secret. The PRF output is
// already uniform, so stretching would add latency and no security; HKDF is
// there for domain separation from the bytes the authenticator hands out.
func PasskeyKEK(prf []byte) (Key, error) {
	if len(prf) != KeyLen {
		return Key{}, fmt.Errorf("crypt: prf secret is %d bytes, want %d", len(prf), KeyLen)
	}
	var raw Key
	copy(raw[:], prf)
	defer raw.Zero()
	return subkey(raw, nil, "lemmary/passkey/v1")
}
