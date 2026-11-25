package models

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// NameKey is the key tags and options are matched on: lowercase, accents
// stripped, letters, digits and & only, so "Müller GmbH" matches "Muller
// G.m.b.H." while "H&M" stays apart from "H.M".
func NameKey(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ToLower(norm.NFD.String(s))
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '&' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
