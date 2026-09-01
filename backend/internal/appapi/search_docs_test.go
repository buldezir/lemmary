package appapi

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
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

	got, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"mine", "tsomeone", "missing"}, MaxTotalChars: 10000})
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
	asSuper, err := readUserDocuments(app, "", ai.ReadRequest{IDs: []string{"mine", "tsomeone"}, MaxTotalChars: 10000})
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

	got, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a", "b"}, MaxTotalChars: 4000})
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

	got, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a", "b"}, MaxTotalChars: 4000})
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
	if _, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a"}}); err == nil {
		t.Fatal("expected an error when no context budget is left")
	}
	got, err := readUserDocuments(app, "me", ai.ReadRequest{MaxTotalChars: 4000})
	if err != nil || len(got) != 0 {
		t.Fatalf("no ids should be a no-op, got %#v err=%v", got, err)
	}
}

// TestReadUserDocumentsOffsetContinuation is the tail of a long document,
// which used to be unreachable: the reader always started at byte 0 and cut at
// the budget, so anything past the first few pages could not be read at all.
// Successive reads at next_offset must reassemble the document byte for byte.
func TestReadUserDocumentsOffsetContinuation(t *testing.T) {
	// Two bytes per rune, so an offset that is not aligned is a real risk.
	// Trimmed, because the reader trims: offsets are into the text it returns,
	// and a test that measured the untrimmed original would be off by one from
	// the first read onwards.
	full := strings.TrimSpace(strings.Repeat("Договір оренди квартири. ", 400))
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "A", full),
	}}

	var rebuilt strings.Builder
	offset := 0
	for reads := 0; ; reads++ {
		if reads > 50 {
			t.Fatal("the read never reached the end of the document")
		}
		got, err := readUserDocuments(app, "me", ai.ReadRequest{
			IDs:           []string{"a"},
			Offset:        offset,
			MaxTotalChars: 1000,
		})
		if err != nil {
			t.Fatalf("readUserDocuments: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("expected one document, got %d", len(got))
		}
		doc := got[0]
		if !utf8.ValidString(doc.Text) {
			t.Fatal("a continued read was cut mid-rune")
		}
		if doc.TotalChars != len(full) {
			t.Fatalf("total_chars = %d, want %d", doc.TotalChars, len(full))
		}
		rebuilt.WriteString(doc.Text)
		if doc.NextOffset == 0 {
			if doc.Truncated {
				t.Fatal("the last slice reported more to come")
			}
			break
		}
		if doc.NextOffset <= offset {
			t.Fatalf("next_offset %d did not advance past %d", doc.NextOffset, offset)
		}
		offset = doc.NextOffset
	}

	if rebuilt.String() != full {
		t.Fatalf("reassembled %d bytes, want %d", rebuilt.Len(), len(full))
	}

	// An offset landing mid-rune is moved forward rather than corrupting the
	// text, and one past the end is simply the end.
	got, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a"}, Offset: 1, MaxTotalChars: 1000})
	if err != nil || !utf8.ValidString(got[0].Text) {
		t.Fatalf("mid-rune offset: %v", err)
	}
	got, err = readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a"}, Offset: len(full) + 500, MaxTotalChars: 1000})
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if got[0].Text != "" || got[0].NextOffset != 0 {
		t.Fatalf("an offset past the end should read nothing, got %+v", got[0])
	}
}

// TestReadUserDocumentsFocusReturnsRelevantExcerpts: the figure a question is
// about is usually in the middle of a long document, which a head-truncated
// read never reaches.
func TestReadUserDocumentsFocusReturnsRelevantExcerpts(t *testing.T) {
	filler := strings.Repeat("Allgemeine Vertragsbedingungen ohne Zahlen. ", 200)
	full := "Mietvertrag Kopf.\n\n" + filler +
		"\n\nDie monatliche Kaltmiete beträgt 1234 EUR.\n\n" + filler +
		"\n\nUnterschrift des Vermieters."
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "Mietvertrag", full),
	}}

	plain, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a"}, MaxTotalChars: 4000})
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if strings.Contains(plain[0].Text, "1234 EUR") {
		t.Fatal("the fixture is not long enough: a plain read already reaches the figure")
	}

	focused, err := readUserDocuments(app, "me", ai.ReadRequest{
		IDs:           []string{"a"},
		Focus:         "Kaltmiete monatlich",
		MaxTotalChars: 4000,
	})
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	doc := focused[0]
	if !strings.Contains(doc.Text, "1234 EUR") {
		t.Fatalf("focus did not reach the figure it named:\n%s", doc.Text)
	}
	if !doc.Excerpted {
		t.Fatal("an assembled read should say it is excerpted")
	}
	if !strings.Contains(doc.Text, "…") || !strings.Contains(doc.Text, "[offset ") {
		t.Fatalf("gaps should be marked with an ellipsis and an offset:\n%s", doc.Text)
	}
	// The head and the tail are always there: what a document is, and what it
	// comes to.
	if !strings.HasPrefix(doc.Text, "Mietvertrag Kopf.") {
		t.Fatalf("the head was dropped:\n%s", doc.Text)
	}
	if !strings.HasSuffix(doc.Text, "Unterschrift des Vermieters.") {
		t.Fatalf("the tail was dropped:\n%s", doc.Text)
	}
	if len(doc.Text) > 4000 {
		t.Fatalf("excerpt of %d bytes overran the 4000 budget", len(doc.Text))
	}
	if doc.TotalChars != len(full) {
		t.Fatalf("total_chars = %d, want %d", doc.TotalChars, len(full))
	}

	// A document that fits is returned whole, focus or not.
	short := stubDocuments{recs: map[string]*core.Record{
		"s": readableDocument("s", "me", "S", "Kaltmiete 500 EUR"),
	}}
	got, err := readUserDocuments(short, "me", ai.ReadRequest{IDs: []string{"s"}, Focus: "Kaltmiete", MaxTotalChars: 4000})
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if got[0].Text != "Kaltmiete 500 EUR" || got[0].Excerpted {
		t.Fatalf("a short document should be returned whole: %+v", got[0])
	}
}

// The ownership boundary has to hold on the focused path too: it is a second
// way into the same records.
func TestReadUserDocumentsFocusKeepsOwnershipCheck(t *testing.T) {
	long := strings.Repeat("Vertrauliche Angaben zur Miete. ", 400)
	app := stubDocuments{recs: map[string]*core.Record{
		"mine":   readableDocument("mine", "me", "Mine", long),
		"theirs": readableDocument("theirs", "someone-else", "Theirs", long),
	}}

	got, err := readUserDocuments(app, "me", ai.ReadRequest{
		IDs:           []string{"mine", "theirs"},
		Focus:         "Miete",
		MaxTotalChars: 4000,
	})
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("a focused read reached another owner's document: %#v", got)
	}
}

// TestDocumentPassagesQuotesTheMatch: a search hit used to carry one Bleve
// highlight fragment, which for a ten-page document is a hint about where the
// answer might be rather than the answer.
func TestDocumentPassagesQuotesTheMatch(t *testing.T) {
	ocr := strings.Repeat("Vorspann ohne Bedeutung. ", 60) +
		"Die monatliche Kaltmiete beträgt 1234 EUR. " +
		strings.Repeat("Mittelteil ohne Bedeutung. ", 60) +
		"Die Kaution beträgt 3702 EUR. " +
		strings.Repeat("Nachspann ohne Bedeutung. ", 60)

	got := documentPassages("doc1", ocr, "Kaltmiete Kaution", nil, nil, 900)
	if len(got) < 2 {
		t.Fatalf("expected several passages, got %#v", got)
	}
	joined := ""
	for _, p := range got {
		joined += p.Text + "\n"
		if p.StartByte < 0 || p.EndByte > len(ocr) || p.EndByte <= p.StartByte {
			t.Fatalf("passage offsets do not point into the document: %+v", p)
		}
	}
	if !strings.Contains(joined, "1234 EUR") || !strings.Contains(joined, "3702 EUR") {
		t.Fatalf("both matches should be quoted:\n%s", joined)
	}

	// A fuzzy match: the query's word is nowhere in the text, so only the
	// index's own highlight knows where it matched.
	fallback := documentPassages("doc1", ocr, "Kaltmiedte", nil, []string{"…Die monatliche Kaltmiete beträgt 1234 EUR.…"}, 900)
	if len(fallback) != 1 || !strings.Contains(fallback[0].Text, "1234 EUR") {
		t.Fatalf("the highlight fallback did not carry the match: %#v", fallback)
	}

	// Nothing to quote at all is not an error; the caller falls back to a
	// plain snippet.
	if got := documentPassages("doc1", ocr, "Selbstbeteiligung", nil, nil, 900); len(got) != 0 {
		t.Fatalf("a term that occurs nowhere produced passages: %#v", got)
	}
}

func TestFragmentChunksRankAndNeverCollideWithRealChunks(t *testing.T) {
	got := fragmentChunks("doc1", []string{"first", "  ", "second"})
	if len(got) != 2 {
		t.Fatalf("blank fragments should be dropped: %#v", got)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("fragments should keep the highlighter's order: %#v", got)
	}
	for _, hit := range got {
		if hit.Ord >= 0 {
			t.Fatalf("a fragment ordinal could collide with a real chunk: %+v", hit)
		}
		if hit.DocumentID != "doc1" {
			t.Fatalf("document id was not carried: %+v", hit)
		}
	}
}
