package mail

import (
	"testing"
)

func TestBuildDateQuery(t *testing.T) {
	q, err := BuildDateQuery("2024-07-01", "2024-07-31")
	if err != nil {
		t.Fatal(err)
	}
	want := "has:attachment after:2024/7/1 before:2024/8/1"
	if q != want {
		t.Fatalf("got %q want %q", q, want)
	}
}

func TestBuildDateQueryInvalid(t *testing.T) {
	if _, err := BuildDateQuery("nope", "2024-07-01"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := BuildDateQuery("2024-07-10", "2024-07-01"); err == nil {
		t.Fatal("expected date order error")
	}
}

func TestParseTriageResponse(t *testing.T) {
	got, err := ParseTriageResponse(`{"import":[{"message_id":"m1","filenames":["a.pdf","a.pdf","logo.png"]},{"message_id":"","filenames":["x.pdf"]},{"message_id":"m2","filenames":[]}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if got[0].MessageID != "m1" || len(got[0].Filenames) != 2 {
		t.Fatalf("%+v", got[0])
	}
}

func TestIsAllowedImportFilename(t *testing.T) {
	if !IsAllowedImportFilename("Invoice.PDF") {
		t.Fatal("pdf should be allowed")
	}
	if IsAllowedImportFilename("note.ics") {
		t.Fatal("ics should be rejected")
	}
}

func TestStripHTML(t *testing.T) {
	got := stripHTML("<p>Hello <b>world</b></p>")
	if got != "Hello world" {
		t.Fatalf("got %q", got)
	}
}
