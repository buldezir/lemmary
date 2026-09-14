// Package strutil holds small string helpers shared across backend packages.
package strutil

import (
	"strings"
	"unicode/utf8"
)

const Ellipsis = "…"

// FirstNonEmpty returns the first value that is non-empty once trimmed, trimmed.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Truncate shortens s to at most maxBytes bytes without splitting a UTF-8 rune.
// Nothing is appended; for byte budgets rather than for display.
func Truncate(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// TruncateRunes trims s and shortens it to maxRunes runes, appending an ellipsis
// when anything was dropped. For human-facing text.
func TruncateRunes(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes <= 0 || s == "" {
		return s
	}
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:maxRunes]) + Ellipsis
}
