package appapi

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/retrieval"
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

	got, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"mine", "tsomeone", "missing"}}, nil, focusExcerptBytes)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(got) != 1 || got[0].ID != "mine" {
		t.Fatalf("expected only the caller's own document, got %#v", got)
	}
	if got[0].Text != "rent is 900 EUR" {
		t.Fatalf("text = %q", got[0].Text)
	}

	// A superuser (empty userID) reaches both, matching the search path.
	asSuper, err := readUserDocuments(app, "", ai.ReadRequest{IDs: []string{"mine", "tsomeone"}}, nil, focusExcerptBytes)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(asSuper) != 2 {
		t.Fatalf("superuser should read both, got %#v", asSuper)
	}
}

// TestReadUserDocumentsReturnsFullTextUnderTheCap: the excerpt size is the
// only thing that shortens a read. Under it the text comes back whole, and
// the cap is the caller's -- the helper's generous one here -- not a guess.
func TestReadUserDocumentsReturnsFullTextUnderTheCap(t *testing.T) {
	long := strings.Repeat("x", 50000)
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "A", long),
		"b": readableDocument("b", "me", "B", long),
	}}

	got, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a", "b"}}, nil, helperInputBytes)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both documents, got %d", len(got))
	}
	for _, doc := range got {
		if doc.Text != long {
			t.Fatalf("%s was shortened: got %d bytes, want %d", doc.ID, len(doc.Text), len(long))
		}
		if doc.Excerpted {
			t.Fatalf("%s was excerpted under the cap: %+v", doc.ID, doc)
		}
	}
}

// TestReadUserDocumentsNeverReturnsAWholeLongDocument: over the cap, a read
// with neither focus nor question still comes back as an excerpt -- the head,
// gap marked -- rather than the whole text. The whole of a long document is
// the one thing the research model is never handed.
func TestReadUserDocumentsNeverReturnsAWholeLongDocument(t *testing.T) {
	long := strings.Repeat("x", 50000)
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "A", long),
	}}

	got, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a"}}, nil, focusExcerptBytes)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(got) != 1 || !got[0].Excerpted || len(got[0].Text) > focusExcerptBytes {
		t.Fatalf("a long unfocused read should be an excerpt within %d bytes, got %d excerpted=%v", focusExcerptBytes, len(got[0].Text), got[0].Excerpted)
	}
	if got[0].FocusUsed != "" {
		t.Fatalf("no question was given, so none should be reported as used: %q", got[0].FocusUsed)
	}
}

// TestReadUserDocumentsReturnsMultibyteTextIntact: neither the whole-text
// path nor the excerpt path may cut a rune in half.
func TestReadUserDocumentsReturnsMultibyteTextIntact(t *testing.T) {
	// Two bytes per rune.
	long := strings.Repeat("а", 50000)
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "A", long),
	}}

	whole, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a"}}, nil, helperInputBytes)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if len(whole) != 1 || whole[0].Text != long {
		t.Fatalf("read %d bytes, want the whole %d", len(whole[0].Text), len(long))
	}
	excerpt, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a"}}, nil, focusExcerptBytes)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if !utf8.ValidString(excerpt[0].Text) {
		t.Fatal("the excerpt was cut mid-rune")
	}
}

func TestReadUserDocumentsNoIDsIsANoOp(t *testing.T) {
	got, err := readUserDocuments(stubDocuments{}, "me", ai.ReadRequest{}, nil, focusExcerptBytes)
	if err != nil || len(got) != 0 {
		t.Fatalf("no ids should be a no-op, got %#v err=%v", got, err)
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

	// The fixture has to be longer than one focused excerpt, or there is
	// nothing for the focus to choose between.
	if len(full) <= focusExcerptBytes {
		t.Fatalf("fixture of %d bytes fits one excerpt of %d", len(full), focusExcerptBytes)
	}
	// No focus, but the user's question: the excerpt is chosen by it, and
	// the document says which question chose it. The whole text is never
	// returned for a document this long.
	plain, err := readUserDocuments(app, "me", ai.ReadRequest{IDs: []string{"a"}, Question: "Wie hoch ist die Kaltmiete monatlich?"}, nil, focusExcerptBytes)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	if plain[0].Text == full || len(plain[0].Text) > focusExcerptBytes {
		t.Fatalf("an unfocused read returned %d bytes of %d; want an excerpt", len(plain[0].Text), len(full))
	}
	if !plain[0].Excerpted || plain[0].FocusUsed != "Wie hoch ist die Kaltmiete monatlich?" {
		t.Fatalf("an unfocused read should say the question chose its excerpt: %+v", plain[0])
	}
	if !strings.Contains(plain[0].Text, "1234 EUR") {
		t.Fatalf("the question did not reach the figure:\n%s", plain[0].Text)
	}

	focused, err := readUserDocuments(app, "me", ai.ReadRequest{
		IDs:   []string{"a"},
		Focus: "Kaltmiete monatlich",
	}, nil, focusExcerptBytes)
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
	if len(doc.Text) > focusExcerptBytes {
		t.Fatalf("excerpt of %d bytes overran the %d-byte excerpt size", len(doc.Text), focusExcerptBytes)
	}
	if len(doc.Text) >= len(full) {
		t.Fatalf("a focused read returned the whole document: %d of %d bytes", len(doc.Text), len(full))
	}

	// A document that fits one excerpt is returned whole, focus or not.
	short := stubDocuments{recs: map[string]*core.Record{
		"s": readableDocument("s", "me", "S", "Kaltmiete 500 EUR"),
	}}
	got, err := readUserDocuments(short, "me", ai.ReadRequest{IDs: []string{"s"}, Focus: "Kaltmiete"}, nil, focusExcerptBytes)
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
	long := strings.Repeat("Vertrauliche Angaben zur Miete. ", 800)
	app := stubDocuments{recs: map[string]*core.Record{
		"mine":   readableDocument("mine", "me", "Mine", long),
		"theirs": readableDocument("theirs", "someone-else", "Theirs", long),
	}}

	got, err := readUserDocuments(app, "me", ai.ReadRequest{
		IDs:   []string{"mine", "theirs"},
		Focus: "Miete",
	}, nil, focusExcerptBytes)
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

	got := documentPassages("doc1", ocr, "Kaltmiete Kaution", nil, nil, nil, 900)
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
	fallback := documentPassages("doc1", ocr, "Kaltmiedte", nil, nil, []string{"…Die monatliche Kaltmiete beträgt 1234 EUR.…"}, 900)
	if len(fallback) != 1 || !strings.Contains(fallback[0].Text, "1234 EUR") {
		t.Fatalf("the highlight fallback did not carry the match: %#v", fallback)
	}

	// Nothing to quote at all is not an error; the caller falls back to a
	// plain snippet.
	if got := documentPassages("doc1", ocr, "Selbstbeteiligung", nil, nil, nil, 900); len(got) != 0 {
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

// TestReadUserDocumentsFocusHonoursRawOffsets pins the one coordinate system
// every offset in the feature is measured in: byte 0 of documents.ocr_text as
// it is stored. Stored chunk boundaries come from chunk.Split over the raw
// column, so a reader that trimmed the text before slicing would quote a
// passage shifted by the length of the leading whitespace -- a few characters
// off the passage whose vector actually matched.
func TestReadUserDocumentsFocusHonoursRawOffsets(t *testing.T) {
	const needle = "Die monatliche Kaltmiete beträgt 1234 EUR."
	filler := strings.Repeat("Allgemeine Vertragsbedingungen ohne Zahlen. ", 200)
	// Leading whitespace, as a scan with a blank first line produces.
	full := "\n\n  Mietvertrag Kopf.\n\n" + filler + "\n\n" + needle + "\n\n" + filler + "\n\nUnterschrift des Vermieters.\n"
	app := stubDocuments{recs: map[string]*core.Record{
		"a": readableDocument("a", "me", "Mietvertrag", full),
	}}

	// The windows the ranker points at are real stored chunks: cut by the same
	// chunker the embedder runs, over the same raw text it stores offsets into.
	pieces, _ := chunk.Split(full, chunk.DefaultOptions())
	at := strings.Index(full, needle)
	want := ""
	var windows []retrieval.Window
	var ranked []retrieval.Ranked
	for i, piece := range pieces {
		windows = append(windows, retrieval.Window{Ord: i, StartByte: piece.Start, EndByte: piece.End})
		// Best first, the way a chunk search returns its hits: the passage the
		// focus is about, then whatever else the chunker cut.
		if want == "" && piece.Start <= at && at+len(needle) <= piece.End {
			want = full[piece.Start:piece.End]
			ranked = append([]retrieval.Ranked{{ID: strconv.Itoa(i), Score: 1}}, ranked...)
			continue
		}
		ranked = append(ranked, retrieval.Ranked{ID: strconv.Itoa(i), Score: 0.1})
	}
	if want == "" {
		t.Fatal("the fixture has no single chunk holding the whole passage")
	}
	rank := func(string, string, string) ([]retrieval.Window, []retrieval.Ranked) {
		return windows, ranked
	}

	got, err := readUserDocuments(app, "me", ai.ReadRequest{
		IDs:   []string{"a"},
		Focus: "Kaltmiete monatlich",
	}, rank, focusExcerptBytes)
	if err != nil {
		t.Fatalf("readUserDocuments: %v", err)
	}
	doc := got[0]
	if !doc.Excerpted {
		t.Fatalf("an assembled read should say it is excerpted: %+v", doc)
	}
	if !strings.Contains(doc.Text, want) {
		t.Fatalf("the excerpt does not quote the chunk the ranking chose.\nwant a slice containing:\n%q\ngot:\n%s", want, doc.Text)
	}
	if !strings.Contains(doc.Text, needle) {
		t.Fatalf("the passage the focus named is missing:\n%s", doc.Text)
	}
}
