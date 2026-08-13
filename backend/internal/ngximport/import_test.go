package ngximport

import "testing"

func TestDocumentDate(t *testing.T) {
	t.Parallel()
	corr := 1
	got := documentDate(ngxDocument{CreatedDate: "2024-01-15", Correspondent: &corr})
	if got != "2024-01-15" {
		t.Fatalf("got %q", got)
	}
	got = documentDate(ngxDocument{Created: "2024-02-03T10:00:00Z"})
	if got != "2024-02-03" {
		t.Fatalf("got %q", got)
	}
}

func TestParseMode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", ModePreserve, false},
		{"preserve", ModePreserve, false},
		{"Preserve", ModePreserve, false},
		{"reprocess", ModeReprocess, false},
		{"REPROCESS", ModeReprocess, false},
		{"full", "", true},
		{"other", "", true},
	}
	for _, tc := range cases {
		got, err := ParseMode(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("ParseMode(%q) expected error", tc.in)
			}
			continue
		}
		if err != nil {
			t.Fatalf("ParseMode(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseMode(%q)=%q want %q", tc.in, got, tc.want)
		}
	}
}
