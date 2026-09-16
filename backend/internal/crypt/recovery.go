package crypt

import (
	"crypto/rand"
	"fmt"
	"strings"
)

// 160 bits, rendered as eight groups of four Crockford base32 characters.
const (
	recoveryBits  = 160
	recoveryBytes = recoveryBits / 8 // 20
	recoveryChars = 32               // 20 bytes -> 32 base32 chars
	recoveryGroup = 4
)

// Omits I, L, O and U so a handwritten code cannot be misread as a digit, and
// so no group can spell an unfortunate word.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewRecoveryCode is the only way back into an account after a password reset,
// which has no access to the old password and so cannot re-wrap the key. Shown
// once, never stored in recoverable form.
func NewRecoveryCode() (string, error) {
	raw := make([]byte, recoveryBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return formatRecoveryCode(encodeCrockford(raw)), nil
}

// RecoveryKEK skips Argon2 on purpose: stretching buys nothing against 160 bits
// of uniform entropy. HKDF is there for the domain separation.
func RecoveryKEK(code string) (Key, error) {
	raw, err := decodeCrockford(code)
	if err != nil {
		return Key{}, err
	}
	if len(raw) != recoveryBytes {
		return Key{}, fmt.Errorf("crypt: recovery code decodes to %d bytes, want %d", len(raw), recoveryBytes)
	}
	var master Key
	copy(master[:], raw)
	defer master.Zero()
	return subkey(master, nil, "lemmary/recovery/v1")
}

// RecoveryHint is the last four characters, for a "code ending in ABCD"
// reminder. Not a verifier; it must never gate anything.
func RecoveryHint(code string) string {
	norm := normalizeRecoveryCode(code)
	if len(norm) < recoveryGroup {
		return ""
	}
	return norm[len(norm)-recoveryGroup:]
}

func formatRecoveryCode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i += recoveryGroup {
		if i > 0 {
			b.WriteByte('-')
		}
		b.WriteString(s[i:min(i+recoveryGroup, len(s))])
	}
	return b.String()
}

// normalizeRecoveryCode drops separators and folds the Crockford confusables,
// so a code read off paper as "O" or "l" still works.
func normalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		switch r {
		case '-', ' ', '\t', '\n', '\r', '_':
			continue
		case 'O':
			b.WriteByte('0')
		case 'I', 'L':
			b.WriteByte('1')
		case 'U':
			// Not in the alphabet and not a digit confusable: passing it through
			// makes decode error rather than silently decode something else.
			b.WriteByte('U')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func encodeCrockford(raw []byte) string {
	var b strings.Builder
	var acc uint32
	var bits uint
	for _, c := range raw {
		acc = acc<<8 | uint32(c)
		bits += 8
		for bits >= 5 {
			bits -= 5
			b.WriteByte(crockford[(acc>>bits)&0x1f])
		}
	}
	if bits > 0 {
		b.WriteByte(crockford[(acc<<(5-bits))&0x1f])
	}
	return b.String()
}

func decodeCrockford(code string) ([]byte, error) {
	norm := normalizeRecoveryCode(code)
	if len(norm) != recoveryChars {
		return nil, fmt.Errorf("crypt: recovery code has %d characters, want %d", len(norm), recoveryChars)
	}
	out := make([]byte, 0, recoveryBytes)
	var acc uint32
	var bits uint
	for _, r := range norm {
		idx := strings.IndexRune(crockford, r)
		if idx < 0 {
			return nil, fmt.Errorf("crypt: invalid character %q in recovery code", r)
		}
		acc = acc<<5 | uint32(idx)
		bits += 5
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(acc>>bits))
		}
	}
	return out, nil
}
