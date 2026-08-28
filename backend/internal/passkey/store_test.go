package passkey

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestEncodeCredentialIDIsUnpaddedBase64URL(t *testing.T) {
	t.Parallel()
	// Bytes chosen so standard base64 would emit both '+' and '/', which the
	// browser's base64url encoding renders as '-' and '_'. Getting this wrong
	// means a credential can never be found again at login.
	raw := []byte{0xfb, 0xff, 0xbe, 0x00, 0x01}
	got := EncodeCredentialID(raw)

	if strings.ContainsAny(got, "+/=") {
		t.Fatalf("EncodeCredentialID(%v) = %q, want no +, / or = characters", raw, got)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("re-decoding %q failed: %v", got, err)
	}
	if string(decoded) != string(raw) {
		t.Fatalf("round trip = %v, want %v", decoded, raw)
	}
}

func TestEncodeCredentialIDEmpty(t *testing.T) {
	t.Parallel()
	if got := EncodeCredentialID(nil); got != "" {
		t.Fatalf("EncodeCredentialID(nil) = %q, want empty", got)
	}
}

func TestNormalizeName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "trims", in: "  Phone  ", want: "Phone"},
		{name: "empty falls back", in: "", want: "Passkey"},
		{name: "whitespace falls back", in: "   ", want: "Passkey"},
		{name: "kept as-is", in: "YubiKey 5C", want: "YubiKey 5C"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeName(tc.in); got != tc.want {
				t.Fatalf("NormalizeName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeNameTruncatesByRuneNotByte(t *testing.T) {
	t.Parallel()
	// The column is 100 characters. Truncating by byte would split a multi-byte
	// rune and store invalid UTF-8.
	long := strings.Repeat("é", 150)
	got := NormalizeName(long)
	if runes := len([]rune(got)); runes != 100 {
		t.Fatalf("length = %d runes, want 100", runes)
	}
	if !strings.HasPrefix(long, got) {
		t.Fatalf("truncation mangled the value: %q", got)
	}
}
