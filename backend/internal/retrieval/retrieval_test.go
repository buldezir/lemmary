package retrieval

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestRRFRanksByReciprocalRank(t *testing.T) {
	// b is second in both lists; a is first in one and absent from the other.
	// Agreement wins: 1/61+1/62 beats 1/61 alone.
	fused := RRF(
		[]Ranked{{ID: "a", Score: 9}, {ID: "b", Score: 8}},
		[]Ranked{{ID: "c", Score: 5}, {ID: "b", Score: 4}},
	)
	if len(fused) != 3 {
		t.Fatalf("fused = %v", fused)
	}
	if fused[0].ID != "b" {
		t.Fatalf("the document both lists found should win: %v", fused)
	}
	want := 1/(RRFConstant+2) + 1/(RRFConstant+2)
	if diff := fused[0].Score - want; diff > 1e-9 || diff < -1e-9 {
		t.Fatalf("score = %v, want %v", fused[0].Score, want)
	}

	// a and c are each rank 1 of one list, so they tie on score. The tiebreak
	// is the earlier list, which keeps the order total and reproducible.
	if fused[1].ID != "a" || fused[2].ID != "c" {
		t.Fatalf("tie was not broken by list order: %v", fused)
	}

	// Scores are never read: a list with huge scores does not beat rank.
	swapped := RRF(
		[]Ranked{{ID: "x", Score: 0.001}},
		[]Ranked{{ID: "y", Score: 1000}},
	)
	if swapped[0].ID != "x" {
		t.Fatalf("fusion read the raw scores: %v", swapped)
	}
}

func TestRRFIgnoresEmptyAndDuplicateEntries(t *testing.T) {
	fused := RRF(nil, []Ranked{{ID: "a"}, {ID: ""}, {ID: "a"}}, nil)
	if len(fused) != 1 || fused[0].ID != "a" {
		t.Fatalf("fused = %v", fused)
	}
	// Repeating an id in one list must not pay for it twice.
	single := RRF([]Ranked{{ID: "a"}})
	if fused[0].Score != single[0].Score {
		t.Fatalf("a duplicate was counted: %v vs %v", fused[0].Score, single[0].Score)
	}
	if got := RRF(); len(got) != 0 {
		t.Fatalf("no lists should fuse to nothing, got %v", got)
	}
}

func TestGroupChunksOrdersDocumentsByBestChunk(t *testing.T) {
	docs, byDoc := GroupChunks([]ChunkHit{
		{DocumentID: "a", Ord: 0, Score: 0.4},
		{DocumentID: "b", Ord: 7, Score: 0.9},
		{DocumentID: "a", Ord: 3, Score: 0.8},
		{DocumentID: "a", Ord: 5, Score: 0.1},
		{DocumentID: "", Ord: 1, Score: 1},
	}, 2)

	if len(docs) != 2 || docs[0].ID != "b" || docs[1].ID != "a" {
		t.Fatalf("documents = %v, want b before a", docs)
	}
	if docs[0].Score != 0.9 || docs[1].Score != 0.8 {
		t.Fatalf("a document should score as its best chunk: %v", docs)
	}
	if len(byDoc["a"]) != 2 {
		t.Fatalf("perDoc was not applied: %v", byDoc["a"])
	}
	if byDoc["a"][0].Ord != 3 {
		t.Fatalf("chunks should be best first: %v", byDoc["a"])
	}
}

func TestSelectPassagesQuotesTheBestChunks(t *testing.T) {
	ocr := "Head of the document. " + strings.Repeat("filler ", 100) +
		"The monthly rent is 1234 EUR. " + strings.Repeat("more filler ", 100) +
		"The deposit is 3702 EUR."
	rentAt := strings.Index(ocr, "The monthly rent")
	depositAt := strings.Index(ocr, "The deposit")

	dense := []ChunkHit{
		{DocumentID: "d", Ord: 1, Score: 0.9, StartByte: rentAt, EndByte: rentAt + 29},
		{DocumentID: "d", Ord: 2, Score: 0.5, StartByte: depositAt, EndByte: len(ocr)},
	}
	lexical := []ChunkHit{
		{DocumentID: "d", Ord: 2, Score: 3, StartByte: depositAt, EndByte: len(ocr)},
	}

	got := SelectPassages(ocr, dense, lexical, 600)
	if len(got) != 2 {
		t.Fatalf("passages = %#v", got)
	}
	// Ord 2 is in both lists; ord 1 is only in the dense one.
	if !strings.Contains(got[0].Text, "deposit is 3702") {
		t.Fatalf("the chunk both retrievers found should come first: %#v", got)
	}
	if !strings.Contains(got[1].Text, "1234 EUR") {
		t.Fatalf("second passage = %q", got[1].Text)
	}
	if got[1].StartByte != rentAt {
		t.Fatalf("offsets were not carried through: %#v", got[1])
	}

	// At most three, however many chunks matched.
	many := make([]ChunkHit, 0, 10)
	for i := 0; i < 10; i++ {
		many = append(many, ChunkHit{DocumentID: "d", Ord: i, Score: 1, Text: "passage " + strconv.Itoa(i)})
	}
	if got := SelectPassages(ocr, nil, many, 600); len(got) != MaxPassagesPerDocument {
		t.Fatalf("got %d passages, want at most %d", len(got), MaxPassagesPerDocument)
	}
}

// Chunk offsets outlive the text they were cut from: a re-OCR replaces the text
// and the stored boundaries then point at the wrong place, or past the end.
// Quoting a clamped slice would be a quotation from nowhere.
func TestSelectPassagesDropsStaleOffsets(t *testing.T) {
	ocr := "Short document."
	got := SelectPassages(ocr, nil, []ChunkHit{
		{DocumentID: "d", Ord: 0, Score: 1, StartByte: 400, EndByte: 900},
		{DocumentID: "d", Ord: 1, Score: 0.5, StartByte: 0, EndByte: 5},
	}, 600)
	if len(got) != 1 || got[0].Text != "Short" {
		t.Fatalf("stale offsets were not dropped: %#v", got)
	}
	if got := SelectPassages(ocr, nil, nil, 600); got != nil {
		t.Fatalf("nothing to select should return nothing, got %#v", got)
	}
	if got := SelectPassages(ocr, nil, []ChunkHit{{DocumentID: "d", Text: "x"}}, 0); got != nil {
		t.Fatalf("a zero budget should quote nothing, got %#v", got)
	}
}

func TestSelectPassagesRespectsTheBudget(t *testing.T) {
	long := strings.Repeat("Договір оренди квартири. ", 100)
	hits := []ChunkHit{
		{DocumentID: "d", Ord: 0, Score: 3, Text: long},
		{DocumentID: "d", Ord: 1, Score: 2, Text: long},
		{DocumentID: "d", Ord: 2, Score: 1, Text: long},
	}
	got := SelectPassages("", nil, hits, 900)
	total := 0
	for _, p := range got {
		total += len(p.Text)
		if !utf8.ValidString(p.Text) {
			t.Fatalf("a passage was cut mid-rune: %q", p.Text)
		}
	}
	if total > 900 {
		t.Fatalf("passages of %d bytes over the 900 budget", total)
	}
	if total == 0 {
		t.Fatal("nothing was quoted at all")
	}
}

func TestPassageBudgetPerDoc(t *testing.T) {
	if got := PassageBudgetPerDoc(6000, 10); got != 600 {
		t.Fatalf("got %d, want 600", got)
	}
	// One hit may not take the whole cap, and twenty may not be starved.
	if got := PassageBudgetPerDoc(6000, 1); got != maxPassageBudget {
		t.Fatalf("got %d, want the %d ceiling", got, maxPassageBudget)
	}
	if got := PassageBudgetPerDoc(6000, 100); got != minPassageBudget {
		t.Fatalf("got %d, want the %d floor", got, minPassageBudget)
	}
	if got := PassageBudgetPerDoc(6000, 0); got != 0 {
		t.Fatalf("no documents should need no budget, got %d", got)
	}
}

func TestWindowsCoverTheTextAndPreferStoredBoundaries(t *testing.T) {
	text := strings.Repeat("Ein Absatz mit etwas Text.\n\n", 200)
	windows := Windows(text, nil)
	if len(windows) < 4 {
		t.Fatalf("expected several windows, got %d", len(windows))
	}
	cursor := 0
	for i, w := range windows {
		if w.StartByte != cursor {
			t.Fatalf("window %d starts at %d, want %d — windows must tile the text", i, w.StartByte, cursor)
		}
		if w.EndByte <= w.StartByte {
			t.Fatalf("window %d does not advance: %+v", i, w)
		}
		if w.Ord != i {
			t.Fatalf("window %d has ord %d", i, w.Ord)
		}
		cursor = w.EndByte
	}
	if cursor != len(text) {
		t.Fatalf("windows covered %d of %d bytes", cursor, len(text))
	}

	stored := []Window{{Ord: 5, StartByte: 10, EndByte: 40}, {Ord: 4, StartByte: 0, EndByte: 10}}
	got := Windows(text, stored)
	if len(got) != 2 || got[0].Ord != 4 {
		t.Fatalf("stored boundaries should be used in document order: %+v", got)
	}
	// Every stored boundary stale: fall back to deriving them rather than
	// refusing to excerpt at all.
	if got := Windows("short", []Window{{StartByte: 900, EndByte: 1000}}); len(got) != 1 || got[0].EndByte != 5 {
		t.Fatalf("stale boundaries should fall back to derived windows: %+v", got)
	}
}

// A run with no whitespace in it at all -- CJK, or a table dumped without
// spaces -- must still terminate, and must not cut a rune in half.
func TestWindowsTerminateWithoutWhitespace(t *testing.T) {
	text := strings.Repeat("日", 4000)
	windows := Windows(text, nil)
	if len(windows) < 2 {
		t.Fatalf("expected several windows, got %d", len(windows))
	}
	for _, w := range windows {
		if !utf8.ValidString(text[w.StartByte:w.EndByte]) {
			t.Fatalf("window %+v cut a rune in half", w)
		}
	}
	if windows[len(windows)-1].EndByte != len(text) {
		t.Fatal("windows did not reach the end of the text")
	}
}

func TestTermOverlapRanksByCoverage(t *testing.T) {
	text := "Erster Absatz über die Miete.\n\n" +
		"Zweiter Absatz über die Kaution und die Miete zusammen.\n\n" +
		"Dritter Absatz über nichts davon."
	windows := []Window{
		{Ord: 0, StartByte: 0, EndByte: 30},
		{Ord: 1, StartByte: 30, EndByte: 86},
		{Ord: 2, StartByte: 86, EndByte: len(text)},
	}
	ranked := TermOverlap(text, windows, "Miete Kaution")
	if len(ranked) != 2 {
		t.Fatalf("ranked = %v, want only the windows that matched", ranked)
	}
	if ranked[0].ID != "1" {
		t.Fatalf("the window covering both terms should win: %v", ranked)
	}
	if got := TermOverlap(text, windows, ""); got != nil {
		t.Fatalf("no focus should rank nothing, got %v", got)
	}
	// Substring matching is what survives morphology without a stemmer.
	if got := TermOverlap(text, windows, "Mieten"); len(got) != 0 {
		t.Fatalf("Mieten is not a substring of Miete: %v", got)
	}
	if got := TermOverlap(text, windows, "Miet"); len(got) == 0 {
		t.Fatal("a shorter form should still match by substring")
	}
}

func TestExcerptKeepsHeadTailAndMarksGaps(t *testing.T) {
	head := "HEAD OF DOCUMENT. "
	filler := strings.Repeat("nothing of interest here. ", 300)
	needle := "THE ANSWER IS 1234 EUR. "
	tail := "END OF DOCUMENT."
	text := head + filler + needle + filler + tail

	windows := Windows(text, nil)
	ranked := TermOverlap(text, windows, "answer 1234")
	got, omitted := Excerpt(text, windows, ranked, 4000)

	if !strings.HasPrefix(got, "HEAD OF DOCUMENT.") {
		t.Fatalf("the head was dropped:\n%s", got)
	}
	if !strings.HasSuffix(got, "END OF DOCUMENT.") {
		t.Fatalf("the tail was dropped:\n%s", got)
	}
	if !strings.Contains(got, "1234 EUR") {
		t.Fatalf("the ranked window was dropped:\n%s", got)
	}
	if !strings.Contains(got, "…") || !strings.Contains(got, "[offset ") {
		t.Fatalf("gaps were not marked:\n%s", got)
	}
	if len(got) > 4000 {
		t.Fatalf("excerpt of %d bytes over the 4000 budget", len(got))
	}
	if omitted != 0 {
		t.Fatalf("nothing should have been omitted at this budget, got %d", omitted)
	}

	// A document that fits comes back whole, with no markers at all.
	whole, omitted := Excerpt("short text", Windows("short text", nil), nil, 4000)
	if whole != "short text" || omitted != 0 {
		t.Fatalf("a short document was excerpted: %q %d", whole, omitted)
	}

	// A budget too small for every matching window reports what it left out.
	_, omitted = Excerpt(text, windows, TermOverlap(text, windows, "nothing interest"), 2200)
	if omitted == 0 {
		t.Fatal("a tight budget should report omitted passages")
	}
}

func TestExcerptSurvivesATinyBudget(t *testing.T) {
	text := strings.Repeat("Договір оренди квартири. ", 200)
	windows := Windows(text, nil)
	got, _ := Excerpt(text, windows, TermOverlap(text, windows, "оренди"), 300)
	if len(got) > 300 {
		t.Fatalf("excerpt of %d bytes over the 300 budget", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("excerpt cut a rune in half")
	}
	if got == "" {
		t.Fatal("a tiny budget still has to return something")
	}
	if got, _ := Excerpt(text, windows, nil, 0); got != "" {
		t.Fatalf("no budget should excerpt nothing, got %q", got)
	}
}

func TestLexicalChunksCentreOnTheMatch(t *testing.T) {
	text := strings.Repeat("Vorspann ohne Bedeutung. ", 80) +
		"Die monatliche Kaltmiete beträgt 1234 EUR. " +
		strings.Repeat("Nachspann ohne Bedeutung. ", 80)

	hits := LexicalChunks("doc1", text, "Kaltmiete", 3)
	if len(hits) == 0 {
		t.Fatal("expected a chunk around the match")
	}
	if hits[0].DocumentID != "doc1" {
		t.Fatalf("document id was not carried: %+v", hits[0])
	}
	if !strings.Contains(hits[0].Text, "1234 EUR") {
		t.Fatalf("the chunk did not centre on the match: %q", hits[0].Text)
	}
	if hits[0].EndByte <= hits[0].StartByte || hits[0].EndByte > len(text) {
		t.Fatalf("offsets are not usable: %+v", hits[0])
	}
	if text[hits[0].StartByte:hits[0].EndByte] == "" {
		t.Fatal("offsets do not slice the text they came from")
	}
	// A term that does not occur produces nothing, which is the signal the
	// caller falls back to the index's own highlight on.
	if got := LexicalChunks("doc1", text, "Selbstbeteiligung", 3); len(got) != 0 {
		t.Fatalf("a term that does not occur produced chunks: %#v", got)
	}
}
