package strutil_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"lemmary/backend/internal/strutil"
)

func TestFirstNonEmpty(t *testing.T) {
	if got := strutil.FirstNonEmpty("", "  ", " value ", "other"); got != "value" {
		t.Fatalf("expected trimmed value, got %q", got)
	}
	if got := strutil.FirstNonEmpty("", "   "); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
	if got := strutil.FirstNonEmpty(); got != "" {
		t.Fatalf("expected empty string for no values, got %q", got)
	}
}

func TestTruncateKeepsValidUTF8(t *testing.T) {
	// Each Cyrillic rune is 2 bytes, so a 5-byte cap lands mid-rune.
	s := "Рахунок"
	got := strutil.Truncate(s, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8, got %q", got)
	}
	if got != "Ра" {
		t.Fatalf("expected %q, got %q", "Ра", got)
	}
}

func TestTruncateShortInput(t *testing.T) {
	if got := strutil.Truncate("abc", 10); got != "abc" {
		t.Fatalf("expected unchanged value, got %q", got)
	}
	if got := strutil.Truncate("abc", 0); got != "" {
		t.Fatalf("expected empty result, got %q", got)
	}
}

func TestTruncateRunesCountsRunesNotBytes(t *testing.T) {
	got := strutil.TruncateRunes("Rechnung für Büromaterial", 8)
	if got != "Rechnung"+strutil.Ellipsis {
		t.Fatalf("unexpected truncation: %q", got)
	}
}

func TestTruncateRunesNoEllipsisWhenShort(t *testing.T) {
	if got := strutil.TruncateRunes("  short  ", 20); got != "short" {
		t.Fatalf("expected trimmed value without ellipsis, got %q", got)
	}
}

func TestTruncateRunesNonPositiveMax(t *testing.T) {
	if got := strutil.TruncateRunes(" keep ", 0); got != "keep" {
		t.Fatalf("expected trimmed value, got %q", got)
	}
}

func TestTruncateRunesAllMultibyte(t *testing.T) {
	got := strutil.TruncateRunes(strings.Repeat("я", 10), 3)
	if utf8.RuneCountInString(got) != 4 { // 3 runes + ellipsis
		t.Fatalf("expected 3 runes plus ellipsis, got %q", got)
	}
}
