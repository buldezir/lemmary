package crypt

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// SaltLen is the size of a per-user KDF salt.
const SaltLen = 16

// Default Argon2id cost. These land at roughly 60-120ms and 64MiB of transient
// allocation per derivation on a modern core, which is the right order for an
// interactive login. It is also why the paperless-ngx HTTP Basic path must cache
// verified passwords rather than derive on every request: Basic auth resends the
// password with each call, and 64MiB per request is a self-inflicted DoS.
const (
	DefaultArgonMemKiB uint32 = 64 * 1024
	DefaultArgonTime   uint32 = 3
	DefaultArgonLanes  uint8  = 4
)

// KDFAlgoArgon2id is the only algorithm this package derives with.
const KDFAlgoArgon2id = "argon2id"

// KDFParams records how a particular user's key-encryption key is derived.
//
// The parameters are stored per user rather than as global constants so the cost
// can be raised later without invalidating existing wraps: an old record keeps
// deriving with the parameters it was written with, and is re-wrapped with the
// new ones the next time its password is set. The JSON keys are short because
// this is persisted in a text column for every user.
type KDFParams struct {
	Algo   string `json:"a"`
	MemKiB uint32 `json:"m"`
	Time   uint32 `json:"t"`
	Lanes  uint8  `json:"p"`
	Salt   []byte `json:"s"`
}

// NewKDFParams returns default parameters with a fresh random salt.
func NewKDFParams() (KDFParams, error) {
	salt := make([]byte, SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return KDFParams{}, err
	}
	return KDFParams{
		Algo:   KDFAlgoArgon2id,
		MemKiB: DefaultArgonMemKiB,
		Time:   DefaultArgonTime,
		Lanes:  DefaultArgonLanes,
		Salt:   salt,
	}, nil
}

// Validate rejects parameters that would derive a weak or unusable key.
//
// This runs on values read back from the database, so it is a trust boundary: an
// operator who can edit the users table could otherwise set memory=1 and turn
// the KDF into a no-op, making offline password guessing cheap.
func (p KDFParams) Validate() error {
	if p.Algo != KDFAlgoArgon2id {
		return fmt.Errorf("crypt: unsupported kdf %q", p.Algo)
	}
	if len(p.Salt) < SaltLen {
		return fmt.Errorf("crypt: kdf salt is %d bytes, need at least %d", len(p.Salt), SaltLen)
	}
	// Floors, not the defaults: parameters written by an older version may be
	// lower than what we would choose today and must keep working.
	if p.MemKiB < 8*1024 {
		return fmt.Errorf("crypt: kdf memory %d KiB is below the minimum", p.MemKiB)
	}
	if p.Time < 1 {
		return fmt.Errorf("crypt: kdf time must be at least 1")
	}
	if p.Lanes < 1 {
		return fmt.Errorf("crypt: kdf lanes must be at least 1")
	}
	return nil
}

// Encode serialises the parameters for storage in a text column.
func (p KDFParams) Encode() (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DecodeKDFParams parses parameters previously produced by Encode.
func DecodeKDFParams(s string) (KDFParams, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return KDFParams{}, fmt.Errorf("crypt: empty kdf parameters")
	}
	var p KDFParams
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return KDFParams{}, fmt.Errorf("crypt: decode kdf parameters: %w", err)
	}
	if err := p.Validate(); err != nil {
		return KDFParams{}, err
	}
	return p, nil
}

// DeriveKEK turns a password into a key-encryption key.
//
// The result only ever wraps a data-encryption key; it never encrypts user data
// directly, so that changing a password re-wraps one small blob instead of
// rewriting every document.
func DeriveKEK(password string, p KDFParams) (Key, error) {
	if err := p.Validate(); err != nil {
		return Key{}, err
	}
	out := argon2.IDKey([]byte(password), p.Salt, p.Time, p.MemKiB, p.Lanes, KeyLen)
	var k Key
	copy(k[:], out)
	clear(out)
	return k, nil
}
