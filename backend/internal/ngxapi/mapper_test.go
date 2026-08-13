package ngxapi

import "testing"

func TestSlugify(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"Bank Statement": "bank-statement",
		"  Invoice #42 ": "invoice-42",
		"":               "",
	}

	for input, want := range cases {
		if got := slugify(input); got != want {
			t.Fatalf("slugify(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestStripHTML(t *testing.T) {
	t.Parallel()

	got := stripHTML("<p>Hello <b>world</b></p>")
	if got != "Hello world" {
		t.Fatalf("stripHTML() = %q", got)
	}
}

func TestMapJobStatus(t *testing.T) {
	t.Parallel()

	if mapJobStatus("completed") != "SUCCESS" {
		t.Fatal("expected SUCCESS for completed")
	}
	if mapJobStatus("pending") != "PENDING" {
		t.Fatal("expected PENDING for pending")
	}
}
