// Package crypt holds the cryptographic primitives used by the encrypted vault.
// It depends only on the standard library and golang.org/x/crypto and knows
// nothing about PocketBase, records or users: callers supply the key and the
// associated data.
//
// Key material and purpose-separated subkeys (derive.go), so no two uses of the
// master key share bytes; Argon2id password stretching (kdf.go) with per-wrap
// cost parameters so they can be raised later; key wrapping (wrap.go), which is
// what lets a credential change re-seal one small blob instead of rewriting the
// archive; and recovery codes (recovery.go).
package crypt

import (
	"crypto/rand"
	"errors"
)

const KeyLen = 32

// Key is an array rather than a slice so it is copied by value and never
// aliases a larger buffer. Go has no mlock and the GC may already have copied
// these bytes, so Zero narrows the window but cannot clear a key from RAM; the
// guarantee made here is only that a key is never written to disk.
type Key [KeyLen]byte

func (k *Key) Zero() {
	clear(k[:])
}

// IsZero means unset: an all-zero key is never one this package generates.
func (k Key) IsZero() bool {
	var zero Key
	return k == zero
}

func NewKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, err
	}
	return k, nil
}

var (
	// ErrNotSealed means the value carries no sentinel or magic. Callers treat
	// it as legacy plaintext and pass it through.
	ErrNotSealed = errors.New("crypt: value is not sealed")

	// ErrCorrupt does not distinguish wrong-key from tampered-with: separating
	// them would tell an attacker which of the two they achieved.
	ErrCorrupt = errors.New("crypt: sealed value failed authentication")
)
