package appapi

import (
	"strings"
	"testing"
	"unicode/utf8"

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
	if total > 4000 {
		t.Fatalf("read %d bytes, over the 4000 budget", total)
	}
}

// TestReadUserDocumentsBudgetsMultibyteTextInBytes is the same test with text
// the ASCII one cannot catch. The budget the research loop hands down counts
// bytes -- len() everywhere in contextBudget -- so truncating to that number of
// *runes* returned twice the bytes for Cyrillic, and the reader silently
// overspent the window that had just been reserved for it.
func TestReadUserDocumentsBudgetsMultibyteTextInBytes(t *testing.T) {
	// Two bytes per rune.
	long := strings.Repeat("а", 50000)
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "A", long),
		"b": readableDocument("b", "me", "B", long),
	}}

	got, err := readUserDocuments(app, "me", []string{"a", "b"}, 4000)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	total := 0
	for _, doc := range got {
		total += len(doc.Text)
		if !utf8.ValidString(doc.Text) {
			t.Fatalf("%s was cut mid-rune", doc.ID)
		}
	}
	if total > 4000 {
		t.Fatalf("read %d bytes, over the 4000 budget", total)
	}
	if total == 0 {
		t.Fatal("nothing was read at all")
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
