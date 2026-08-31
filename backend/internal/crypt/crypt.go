// Package crypt holds the cryptographic primitives used by the encrypted
// vault. It deliberately depends only on the standard library and
// golang.org/x/crypto so it can be unit-tested in isolation, and it knows
// nothing about PocketBase, records or users: callers supply the key and the
// associated data, and this package supplies the bytes.
//
// It provides four things:
//
//   - Key material (this file) and purpose-separated subkey derivation
//     (derive.go), so no two uses of the master key ever share bytes.
//   - Argon2id password stretching (kdf.go), with the cost parameters stored
//     alongside each wrap so they can be raised later without invalidating
//     what already exists.
//   - Key wrapping (wrap.go): the master key sealed under a credential-derived
//     key-encryption key. Wrapping rather than encrypting directly is what lets
//     a credential change re-seal one small blob instead of rewriting the
//     archive.
//   - Recovery codes (recovery.go), the way back in when every other credential
//     is gone.
package crypt

import (
	"crypto/rand"
	"errors"
)

// KeyLen is the size of a data-encryption key in bytes.
const KeyLen = 32

// Key is a symmetric key held in memory.
//
// It is an array rather than a slice so that it is copied by value and never
// aliases a larger buffer. Note the honest limit on "held in memory": Go offers
// no mlock and the garbage collector may already have copied these bytes while
// moving the containing object, so Zero below narrows the window but cannot
// guarantee the key is gone from RAM. The guarantee this package does make is
// narrower and worth stating plainly: the key is never written to disk.
type Key [KeyLen]byte

// Zero overwrites the key in place.
func (k *Key) Zero() {
	clear(k[:])
}

// IsZero reports whether the key is all zero bytes, which is never a key this
// package generates and therefore signals "unset".
func (k Key) IsZero() bool {
	var zero Key
	return k == zero
}

// NewKey returns a fresh random key.
func NewKey() (Key, error) {
	var k Key
	if _, err := rand.Read(k[:]); err != nil {
		return Key{}, err
	}
	return k, nil
}

var (
	// ErrNotSealed reports that a value does not carry this package's sentinel
	// or magic. Callers treat it as "legacy plaintext, pass through".
	ErrNotSealed = errors.New("crypt: value is not sealed")

	// ErrCorrupt reports a sealed value that failed authentication, or whose
	// framing is inconsistent. It deliberately does not distinguish wrong-key
	// from tampered-with: both are the same failure to a caller, and separating
	// them would tell an attacker which of the two they achieved.
	ErrCorrupt = errors.New("crypt: sealed value failed authentication")
)
