package ngxapi

import "testing"

func TestCreatedDateOnly(t *testing.T) {
	t.Parallel()

	if got := createdDateOnly("2024-05-01 12:00:00.000Z"); got != "2024-05-01" {
		t.Fatalf("createdDateOnly() = %q", got)
	}
}

func TestFormatNgxDateTime(t *testing.T) {
	t.Parallel()

	got := formatNgxDateTime("2026-06-13 11:56:31.599Z")
	want := "2026-06-13T11:56:31.599Z"
	if got != want {
		t.Fatalf("formatNgxDateTime() = %q, want %q", got, want)
	}
}

func TestFormatNgxCreatedDate(t *testing.T) {
	t.Parallel()

	if got := formatNgxCreatedDate("2024-03-15"); got != "2024-03-15T00:00:00Z" {
		t.Fatalf("formatNgxCreatedDate(date) = %q", got)
	}
	if got := formatNgxCreatedDate("2025-12-19 00:00:00.000Z"); got != "2025-12-19T00:00:00.000Z" {
		t.Fatalf("formatNgxCreatedDate(datetime) = %q", got)
	}
}
