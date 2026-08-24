package models

import (
	"strings"
	"time"
	"unicode"
)

const documentDateLayout = "2006-01-02"

// documentDateLayouts are tried in order. Ordering carries meaning:
//
//   - Canonical and ISO-ish forms come first so a well-behaved model costs one
//     Parse call.
//   - Day-first forms (02.01.2006) precede month-first ones (01/02/2006).
//     Numeric slash/dot dates are genuinely ambiguous, and this archive skews
//     European, so 05/06/2026 is read as 5 June. time.Parse range-checks its
//     fields, so an unambiguous US date like 03/15/2026 fails day-first (month
//     15 does not exist) and falls through to the month-first layouts.
//   - Partial forms come last: a year-month or bare year is coerced to the
//     first day of that period rather than dropped, which keeps the signal the
//     document actually carries.
//
// Only English month names are understood — time.Parse has no locale support,
// and the extraction prompt asks for numeric YYYY-MM-DD anyway.
var documentDateLayouts = []string{
	documentDateLayout,
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.000Z",
	"2006-01-02 15:04:05Z",
	"2006-01-02 15:04:05",
	"2006/01/02",
	"2006.01.02",
	"20060102",

	"02.01.2006",
	"02/01/2006",
	"02-01-2006",
	"2.1.2006",
	"2/1/2006",
	"2-1-2006",

	"01/02/2006",
	"01-02-2006",
	"1/2/2006",
	"1-2-2006",

	"2 January 2006",
	"2 Jan 2006",
	"January 2, 2006",
	"Jan 2, 2006",

	"2006-01",
	"2006/01",
	"01/2006",
	"01.2006",

	"2006",
}

const (
	minDocumentDateYear = 1000
	maxDocumentDateYear = 3000
)

// NormalizeDocumentDate coerces a model-supplied date into the canonical
// YYYY-MM-DD form documents.document_date expects. It reports false when the
// value cannot be understood at all, so callers can drop it instead of
// discarding a whole extraction over one optional field. An empty value is not
// a failure: there is simply nothing to repair.
func NormalizeDocumentDate(raw string) (string, bool) {
	raw = cleanDocumentDate(raw)
	if raw == "" {
		return "", true
	}
	for _, layout := range documentDateLayouts {
		t, err := time.Parse(layout, raw)
		if err != nil {
			continue
		}
		if year := t.Year(); year < minDocumentDateYear || year > maxDocumentDateYear {
			// A layout matched but produced nonsense (e.g. "0000-00-00"
			// normalized by Go into year 0). Treat it as unusable rather than
			// storing a date no document could carry.
			return "", false
		}
		return t.Format(documentDateLayout), true
	}
	return "", false
}

// cleanDocumentDate trims the value, drops quotes a model may have wrapped
// around it, and collapses internal whitespace so "15  March   2026" still
// matches a layout.
func cleanDocumentDate(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.Trim(raw, `"'`)

	var b strings.Builder
	b.Grow(len(raw))
	prevSpace := false
	for _, r := range raw {
		if unicode.IsSpace(r) {
			if b.Len() > 0 && !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return strings.TrimSpace(b.String())
}
