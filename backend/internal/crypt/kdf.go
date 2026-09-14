package crypt

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const SaltLen = 16

// Roughly 60-120ms and 64MiB of transient allocation per derivation, the right
// order for an interactive login. It is also why the paperless-ngx HTTP Basic
// path caches verified passwords: Basic auth resends the password on every
// call, and 64MiB per request is a self-inflicted DoS.
const (
	DefaultArgonMemKiB uint32 = 64 * 1024
	DefaultArgonTime   uint32 = 3
	DefaultArgonLanes  uint8  = 4
)

const KDFAlgoArgon2id = "argon2id"

// KDFParams is stored per user rather than as global constants so the cost can
// be raised without invalidating existing wraps: an old record keeps deriving
// with what it was written with until its password is next set. The JSON keys
// are short because this sits in a text column on every user.
type KDFParams struct {
	Algo   string `json:"a"`
	MemKiB uint32 `json:"m"`
	Time   uint32 `json:"t"`
	Lanes  uint8  `json:"p"`
	Salt   []byte `json:"s"`
}

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

// Validate is a trust boundary: these values come back from the database, and
// an operator who could set memory=1 would turn the KDF into a no-op.
func (p KDFParams) Validate() error {
	if p.Algo != KDFAlgoArgon2id {
		return fmt.Errorf("crypt: unsupported kdf %q", p.Algo)
	}
	if len(p.Salt) < SaltLen {
		return fmt.Errorf("crypt: kdf salt is %d bytes, need at least %d", len(p.Salt), SaltLen)
	}
	// Floors, not the defaults: an older version's parameters must keep working.
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

func (p KDFParams) Encode() (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

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

// DeriveKEK's result only ever wraps a data-encryption key, never user data, so
// a password change re-wraps one small blob instead of every document.
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
