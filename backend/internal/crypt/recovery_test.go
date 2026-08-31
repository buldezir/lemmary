package crypt

import (
	"errors"
	"strings"
	"testing"
)

func TestNewRecoveryCodeShape(t *testing.T) {
	code, err := NewRecoveryCode()
	if err != nil {
		t.Fatalf("NewRecoveryCode: %v", err)
	}
	groups := strings.Split(code, "-")
	if len(groups) != recoveryChars/recoveryGroup {
		t.Fatalf("got %d groups (%q), want %d", len(groups), code, recoveryChars/recoveryGroup)
	}
	for _, g := range groups {
		if len(g) != recoveryGroup {
			t.Fatalf("group %q is %d chars, want %d", g, len(g), recoveryGroup)
		}
	}
	// The excluded letters are the whole point of Crockford: none may appear.
	for _, bad := range []string{"I", "L", "O", "U"} {
		if strings.Contains(code, bad) {
			t.Fatalf("code %q contains ambiguous character %s", code, bad)
		}
	}
}

func TestRecoveryCodesAreDistinct(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		code, err := NewRecoveryCode()
		if err != nil {
			t.Fatalf("NewRecoveryCode: %v", err)
		}
		if seen[code] {
			t.Fatalf("duplicate recovery code %q", code)
		}
		seen[code] = true
	}
}

func TestRecoveryKEKIsStableAcrossTranscriptionVariants(t *testing.T) {
	code, err := NewRecoveryCode()
	if err != nil {
		t.Fatalf("NewRecoveryCode: %v", err)
	}
	want, err := RecoveryKEK(code)
	if err != nil {
		t.Fatalf("RecoveryKEK: %v", err)
	}

	// Everything a person might plausibly type off a printed code.
	variants := []string{
		code,
		strings.ToLower(code),
		strings.ReplaceAll(code, "-", ""),
		strings.ReplaceAll(code, "-", " "),
		"  " + code + "  ",
	}
	for _, v := range variants {
		got, err := RecoveryKEK(v)
		if err != nil {
			t.Fatalf("RecoveryKEK(%q): %v", v, err)
		}
		if got != want {
			t.Fatalf("variant %q derived a different key", v)
		}
	}
}

// A code read off paper with O for 0 or l for 1 must still unlock the account.
func TestRecoveryKEKFoldsConfusableCharacters(t *testing.T) {
	base := "0123-4567-89AB-CDEF-GHJK-MNPQ-RSTV-WXYZ"
	want, err := RecoveryKEK(base)
	if err != nil {
		t.Fatalf("RecoveryKEK: %v", err)
	}
	// Leading "0123" retyped as "O123", and "1" as "l"/"I".
	for _, v := range []string{
		"O123-4567-89AB-CDEF-GHJK-MNPQ-RSTV-WXYZ",
		"0l23-4567-89AB-CDEF-GHJK-MNPQ-RSTV-WXYZ",
		"0I23-4567-89AB-CDEF-GHJK-MNPQ-RSTV-WXYZ",
	} {
		got, err := RecoveryKEK(v)
		if err != nil {
			t.Fatalf("RecoveryKEK(%q): %v", v, err)
		}
		if got != want {
			t.Fatalf("confusable variant %q derived a different key", v)
		}
	}
}

func TestRecoveryKEKRejectsMalformed(t *testing.T) {
	for name, code := range map[string]string{
		"empty":       "",
		"too short":   "0123-4567",
		"too long":    "0123-4567-89AB-CDEF-GHJK-MNPQ-RSTV-WXYZ-0000",
		"bad char U":  "U123-4567-89AB-CDEF-GHJK-MNPQ-RSTV-WXYZ",
		"punctuation": "0123-4567-89AB-CDEF-GHJK-MNPQ-RSTV-WXY!",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RecoveryKEK(code); err == nil {
				t.Fatalf("expected rejection of %q", code)
			}
		})
	}
}

func TestRecoveryKEKIsCodeSpecific(t *testing.T) {
	a, err := NewRecoveryCode()
	if err != nil {
		t.Fatalf("NewRecoveryCode: %v", err)
	}
	b, err := NewRecoveryCode()
	if err != nil {
		t.Fatalf("NewRecoveryCode: %v", err)
	}
	ka, err := RecoveryKEK(a)
	if err != nil {
		t.Fatalf("RecoveryKEK: %v", err)
	}
	kb, err := RecoveryKEK(b)
	if err != nil {
		t.Fatalf("RecoveryKEK: %v", err)
	}
	if ka == kb {
		t.Fatal("distinct recovery codes derived the same key")
	}
}

// The end-to-end shape of the reset path: the DEK is wrapped under both a
// password and a recovery code, and the recovery slot still opens it after the
// password wrap has become undecryptable.
func aadFor(slot, id string) string { return "wrap|" + slot + "|" + id }

func TestRecoverySlotOpensTheSameDEK(t *testing.T) {
	dek := mustKey(t)
	userID := "user1"

	code, err := NewRecoveryCode()
	if err != nil {
		t.Fatalf("NewRecoveryCode: %v", err)
	}
	rcKEK, err := RecoveryKEK(code)
	if err != nil {
		t.Fatalf("RecoveryKEK: %v", err)
	}
	rcWrap, err := WrapKey(rcKEK, dek, aadFor("rc", userID))
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}

	pwKEK, err := DeriveKEK("forgotten", testKDF(t))
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	pwWrap, err := WrapKey(pwKEK, dek, aadFor("pw", userID))
	if err != nil {
		t.Fatalf("WrapKey: %v", err)
	}

	// After a reset the old password is gone, so its wrap can no longer be opened.
	newKEK, err := DeriveKEK("brand new", testKDF(t))
	if err != nil {
		t.Fatalf("DeriveKEK: %v", err)
	}
	if _, err := UnwrapKey(newKEK, pwWrap, aadFor("pw", userID)); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("post-reset password wrap opened: %v", err)
	}

	// The recovery slot is the way back, and it must yield the identical key so
	// existing documents still decrypt.
	recovered, err := UnwrapKey(rcKEK, rcWrap, aadFor("rc", userID))
	if err != nil {
		t.Fatalf("recovery unwrap failed: %v", err)
	}
	if recovered != dek {
		t.Fatal("recovery slot yielded a different key")
	}
	if KeyID(recovered) != KeyID(dek) {
		t.Fatal("recovered key reports a different key id")
	}
}

func TestRecoveryHint(t *testing.T) {
	if got := RecoveryHint("0123-4567-89AB-CDEF-GHJK-MNPQ-RSTV-WXYZ"); got != "WXYZ" {
		t.Fatalf("RecoveryHint = %q, want WXYZ", got)
	}
	if got := RecoveryHint("ab"); got != "" {
		t.Fatalf("RecoveryHint of a short code = %q, want empty", got)
	}
}
