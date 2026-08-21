// Package strutil holds small string helpers shared across backend packages.
package strutil

import (
	"strings"
	"unicode/utf8"
)

// Ellipsis marks a value that was shortened.
const Ellipsis = "…"

// FirstNonEmpty returns the first value that is non-empty once trimmed.
// The returned value is trimmed; "" when every value is blank.
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// Truncate shortens s to at most maxBytes bytes without splitting a UTF-8 rune,
// so the result is always valid UTF-8. Nothing is appended; use this for byte
// budgets (API payload caps, column widths) rather than for display.
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

// TruncateRunes trims s and shortens it to at most maxRunes runes, appending an
// ellipsis when anything was dropped. Use this for human-facing text.
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
