package models_test

import (
	"testing"

	"lemmary/backend/internal/models"
)

func TestNormalizeDocumentDateAccepted(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"canonical", "2026-03-15", "2026-03-15"},
		{"padded and quoted", ` "2026-03-15" `, "2026-03-15"},
		{"iso datetime", "2026-03-15T10:00:00Z", "2026-03-15"},
		{"iso datetime without zone", "2026-03-15T10:00:00", "2026-03-15"},
		{"pocketbase timestamp", "2026-03-15 00:00:00.000Z", "2026-03-15"},
		{"slashed year first", "2026/03/15", "2026-03-15"},
		{"dotted year first", "2026.03.15", "2026-03-15"},
		{"compact", "20260315", "2026-03-15"},
		{"german dotted", "15.03.2026", "2026-03-15"},
		{"german dotted unpadded", "5.3.2026", "2026-03-05"},
		{"day first slashed", "15/03/2026", "2026-03-15"},
		{"day first dashed", "15-03-2026", "2026-03-15"},
		{"ambiguous prefers day first", "05/06/2026", "2026-06-05"},
		{"us slashed falls back to month first", "03/15/2026", "2026-03-15"},
		{"us slashed unpadded", "3/15/2026", "2026-03-15"},
		{"textual day first", "15 March 2026", "2026-03-15"},
		{"textual short month", "15 Mar 2026", "2026-03-15"},
		{"textual month first", "March 15, 2026", "2026-03-15"},
		{"textual short month first", "Mar 15, 2026", "2026-03-15"},
		{"collapses inner whitespace", "15  March   2026", "2026-03-15"},
		// The issue #28 cases: partial dates coerce to the first of the period
		// instead of failing the whole extraction.
		{"bare year", "2026", "2026-01-01"},
		{"year and month", "2026-03", "2026-03-01"},
		{"year and month slashed", "2026/03", "2026-03-01"},
		{"month and year", "03/2026", "2026-03-01"},
		{"month and year dotted", "03.2026", "2026-03-01"},
		{"empty stays empty", "", ""},
		{"blank stays empty", "   ", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := models.NormalizeDocumentDate(tc.in)
			if !ok {
				t.Fatalf("NormalizeDocumentDate(%q) reported failure, want %q", tc.in, tc.want)
			}
			if got != tc.want {
				t.Fatalf("NormalizeDocumentDate(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeDocumentDateRejected(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"prose", "sometime last spring"},
		{"not a date", "not a date"},
		{"month out of range", "2026-13-01"},
		{"day out of range", "2026-02-30"},
		{"all zeroes", "0000-00-00"},
		{"two digit year", "99"},
		{"year before the sanity floor", "0500-03-15"},
		{"year after the sanity ceiling", "3500-03-15"},
		{"trailing prose", "2026-03-15 or thereabouts"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := models.NormalizeDocumentDate(tc.in)
			if ok {
				t.Fatalf("NormalizeDocumentDate(%q) = %q, ok; want rejection", tc.in, got)
			}
			if got != "" {
				t.Fatalf("NormalizeDocumentDate(%q) returned %q on rejection, want empty", tc.in, got)
			}
		})
	}
}
