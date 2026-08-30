package appapi

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"lemmary/backend/internal/strutil"
)

func TestOCRSnippet(t *testing.T) {
	ocr := "Preface text. The plumber invoice for the leak was paid in July. Trailing notes."
	got := ocrSnippet(ocr, "plumber invoice")
	if got == "" {
		t.Fatal("expected snippet")
	}
	if !strings.Contains(strings.ToLower(got), "plumber") {
		t.Fatalf("expected plumber in snippet, got %q", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	got := strutil.TruncateRunes("abcdefghij", 5)
	if got != "abcde…" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeTagNames(t *testing.T) {
	got := normalizeTagNames([]string{" Invoice ", "", "plumbing", "invoice"})
	if len(got) != 2 {
		t.Fatalf("expected 2 unique tags, got %v", got)
	}
	if got[0] != "Invoice" || got[1] != "plumbing" {
		t.Fatalf("unexpected order/values: %v", got)
	}
}

func readableDocument(id, user, title, ocr string) *core.Record {
	col := core.NewBaseCollection("documents")
	col.Fields.Add(
		&core.TextField{Name: "user"},
		&core.TextField{Name: "title"},
		&core.TextField{Name: "ocr_text"},
		&core.TextField{Name: "document_date"},
		&core.TextField{Name: "document_type"},
		&core.TextField{Name: "correspondent"},
		&core.JSONField{Name: "tags"},
	)
	rec := core.NewRecord(col)
	rec.Id = id
	rec.Set("user", user)
	rec.Set("title", title)
	rec.Set("ocr_text", ocr)
	return rec
}

func TestReadUserDocumentsRefusesAnotherOwnersDocument(t *testing.T) {
	app := stubDocuments{recs: map[string]*core.Record{
		"mine":     readableDocument("mine", "me", "My lease", "rent is 900 EUR"),
		"tsomeone": readableDocument("tsomeone", "someone-else", "Their lease", "rent is 700 EUR"),
	}}

	got, err := readUserDocuments(app, "me", []string{"mine", "tsomeone", "missing"}, 10000)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("expected only the caller's own document, got %#v", got)
	}
	if got[0].Text != "rent is 900 EUR" {
		t.Fatalf("text = %q", got[0].Text)
	}
	if got[0].Truncated {
		t.Fatal("short document should not be marked truncated")
	}

	// A superuser (empty userID) reaches both, matching the search path.
	asSuper, err := readUserDocuments(app, "", []string{"mine", "tsomeone"}, 10000)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(asSuper) != 2 {
		t.Fatalf("superuser should read both, got %#v", asSuper)
	}
}

func TestReadUserDocumentsDividesTheBudget(t *testing.T) {
	long := strings.Repeat("x", 50000)
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "A", long),
		"b": readableDocument("b", "me", "B", long),
	}}

	got, err := readUserDocuments(app, "me", []string{"a", "b"}, 4000)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both documents, got %d", len(got))
	}
	total := 0
	for _, doc := range got {
		if !doc.Truncated {
			t.Fatalf("%s should be marked truncated", doc.ID)
		}
		total += len(doc.Text)
	}
	// The ellipsis TruncateRunes appends is the only overshoot allowed.
	if total > 4000+2*len(strutil.Ellipsis) {
		t.Fatalf("read %d chars, over the 4000 budget", total)
	}
}

func TestReadUserDocumentsRejectsAnEmptyBudget(t *testing.T) {
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "A", "text"),
	}}
	if _, err := readUserDocuments(app, "me", []string{"a"}, 0); err == nil {
		t.Fatal("expected an error when no context budget is left")
	}
	got, err := readUserDocuments(app, "me", nil, 4000)
	if err != nil || len(got) != 0 {
		t.Fatalf("no ids should be a no-op, got %#v err=%v", got, err)
	}
}
