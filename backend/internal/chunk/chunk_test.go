package chunk

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func sampleText(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for i := 0; b.Len() < 30000; i++ {
		b.WriteString("Die Rechnung wurde am 3. Januar bezahlt. Der Betrag lautet 128,40 EUR. ")
		if i%7 == 6 {
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func TestSplitIsDeterministic(t *testing.T) {
	t.Parallel()
	text := sampleText(t)

	first, truncFirst := Split(text, DefaultOptions())
	second, truncSecond := Split(text, DefaultOptions())

	if truncFirst != truncSecond {
		t.Fatalf("truncated flag differs between runs: %v vs %v", truncFirst, truncSecond)
	}
	if len(first) != len(second) {
		t.Fatalf("chunk count differs between runs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("chunk %d differs between runs: %+v vs %+v", i, first[i], second[i])
		}
	}
}

// The stored range is sliced out of the live ocr_text column later, so every
// offset has to be a valid rune boundary in the original string.
func TestSplitCutsOnlyAtRuneBoundaries(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("héllo 🌍 世界 привіт ", 900)

	chunks, _ := Split(text, DefaultOptions())
	if len(chunks) < 3 {
		t.Fatalf("want several chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Start < 0 || c.End > len(text) || c.Start >= c.End {
			t.Fatalf("chunk %d out of range: %+v (len %d)", i, c, len(text))
		}
		if !utf8.RuneStart(text[c.Start]) {
			t.Fatalf("chunk %d starts mid-rune at %d", i, c.Start)
		}
		if c.End < len(text) && !utf8.RuneStart(text[c.End]) {
			t.Fatalf("chunk %d ends mid-rune at %d", i, c.End)
		}
		if !utf8.ValidString(text[c.Start:c.End]) {
			t.Fatalf("chunk %d is not valid UTF-8", i)
		}
	}
}

// Every byte of the document must belong to at least one chunk, or a passage
// simply cannot be retrieved.
func TestSplitCoversTheWholeText(t *testing.T) {
	t.Parallel()
	text := sampleText(t)

	chunks, truncated := Split(text, DefaultOptions())
	if truncated {
		t.Fatalf("sample should fit under MaxChunks")
	}
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	if chunks[0].Start != 0 {
		t.Fatalf("first chunk starts at %d, want 0", chunks[0].Start)
	}
	if last := chunks[len(chunks)-1]; last.End != len(text) {
		t.Fatalf("last chunk ends at %d, want %d", last.End, len(text))
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].Start > chunks[i-1].End {
			t.Fatalf("gap between chunk %d and %d: %+v %+v", i-1, i, chunks[i-1], chunks[i])
		}
		if chunks[i].Start <= chunks[i-1].Start {
			t.Fatalf("cursor did not advance at chunk %d: %+v %+v", i, chunks[i-1], chunks[i])
		}
	}
}

func TestSplitOverlapsConsecutiveChunks(t *testing.T) {
	t.Parallel()
	text := sampleText(t)

	chunks, _ := Split(text, DefaultOptions())
	if len(chunks) < 3 {
		t.Fatalf("want several chunks, got %d", len(chunks))
	}
	overlapped := 0
	for i := 1; i < len(chunks); i++ {
		if chunks[i].Start < chunks[i-1].End {
			overlapped++
		}
	}
	if overlapped != len(chunks)-1 {
		t.Fatalf("%d of %d boundaries overlap; want all", overlapped, len(chunks)-1)
	}
}

func TestSplitRespectsMaxRunes(t *testing.T) {
	t.Parallel()
	text := sampleText(t)
	opts := DefaultOptions()

	chunks, _ := Split(text, opts)
	for i, c := range chunks {
		if n := utf8.RuneCountInString(text[c.Start:c.End]); n > opts.MaxRunes {
			t.Fatalf("chunk %d is %d runes, over the %d cap", i, n, opts.MaxRunes)
		}
	}
}

func TestSplitStopsAtMaxChunksAndReportsIt(t *testing.T) {
	t.Parallel()
	text := sampleText(t)
	opts := DefaultOptions()
	opts.MaxChunks = 3

	chunks, truncated := Split(text, opts)
	if len(chunks) != 3 {
		t.Fatalf("got %d chunks, want 3", len(chunks))
	}
	if !truncated {
		t.Fatal("truncated should be true when the cap cut the document short")
	}
}

// A text that ends exactly on the cap is complete, not truncated.
func TestSplitNotTruncatedWhenCapIsReachedAtTheEnd(t *testing.T) {
	t.Parallel()
	text := sampleText(t)
	opts := DefaultOptions()

	chunks, _ := Split(text, opts)
	opts.MaxChunks = len(chunks)

	again, truncated := Split(text, opts)
	if len(again) != len(chunks) {
		t.Fatalf("chunk count changed with an exact cap: %d vs %d", len(again), len(chunks))
	}
	if truncated {
		t.Fatal("a document that ends on the cap is not truncated")
	}
}

func TestSplitWhitespaceOnlyYieldsNothing(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"", "   ", "\n\n\t \n"} {
		chunks, truncated := Split(text, DefaultOptions())
		if len(chunks) != 0 || truncated {
			t.Fatalf("Split(%q) = %v, %v; want no chunks", text, chunks, truncated)
		}
	}
}

// A run with no whitespace at all has no cut candidate; the pass must still
// terminate, falling back to the rune boundary at MaxRunes.
func TestSplitTerminatesWithoutWhitespace(t *testing.T) {
	t.Parallel()
	text := strings.Repeat("a", 10000)

	chunks, truncated := Split(text, DefaultOptions())
	if truncated {
		t.Fatal("10k runes should fit under MaxChunks")
	}
	if len(chunks) < 7 {
		t.Fatalf("got %d chunks for 10000 runes, want at least 7", len(chunks))
	}
	if chunks[len(chunks)-1].End != len(text) {
		t.Fatalf("last chunk ends at %d, want %d", chunks[len(chunks)-1].End, len(text))
	}
}

func TestSplitShortTextIsOneChunk(t *testing.T) {
	t.Parallel()
	text := "A short note about the boiler service."

	chunks, truncated := Split(text, DefaultOptions())
	if truncated {
		t.Fatal("short text is not truncated")
	}
	if len(chunks) != 1 || chunks[0].Start != 0 || chunks[0].End != len(text) {
		t.Fatalf("got %+v, want one chunk covering the text", chunks)
	}
}

func TestSplitPrefersParagraphBreaks(t *testing.T) {
	t.Parallel()
	// Two paragraphs of ~900 runes each: the break sits inside the cut window,
	// so it must be chosen over any of the sentence ends around it.
	first := strings.Repeat("word ", 180)
	second := strings.Repeat("other ", 200)
	text := first + "\n\n" + second

	chunks, _ := Split(text, DefaultOptions())
	if len(chunks) < 2 {
		t.Fatalf("want at least two chunks, got %d", len(chunks))
	}
	wantEnd := len(first) + 2
	if chunks[0].End != wantEnd {
		t.Fatalf("first chunk ends at %d, want the paragraph break at %d", chunks[0].End, wantEnd)
	}
}

// The zero Options must behave, so a caller cannot accidentally build a
// chunker that loops or emits one chunk per rune.
func TestSplitNormalizesZeroOptions(t *testing.T) {
	t.Parallel()
	text := sampleText(t)

	zero, _ := Split(text, Options{})
	std, _ := Split(text, DefaultOptions())
	if len(zero) != len(std) {
		t.Fatalf("zero Options produced %d chunks, defaults %d", len(zero), len(std))
	}
}

func TestHeaderTextRendersLabelledMetadata(t *testing.T) {
	t.Parallel()
	h := Header{
		Title:         "Stromrechnung Januar",
		TitleOriginal: "Stromrechnung Januar",
		Purpose:       "Monthly electricity bill",
		Summary:       "128,40 EUR due on 3 February.",
		DocumentType:  "Invoice",
		Correspondent: "Stadtwerke",
		Date:          "2026-01-31",
		Tags:          []string{"utilities", " ", "electricity"},
		People:        []string{"Anna Muster"},
	}

	text := h.Text()
	for _, want := range []string{
		"Title: Stromrechnung Januar",
		"Type: Invoice",
		"Correspondent: Stadtwerke",
		"Date: 2026-01-31",
		"Tags: utilities, electricity",
		"People or organizations: Anna Muster",
		"Purpose: Monthly electricity bill",
		"Summary: 128,40 EUR due on 3 February.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("header text is missing %q:\n%s", want, text)
		}
	}
	// The original title equals the title, so repeating it would just pay for
	// the same words twice.
	if strings.Contains(text, "Original title") {
		t.Fatalf("identical original title should not be repeated:\n%s", text)
	}
}

func TestHeaderTextEmptyWhenNoMetadata(t *testing.T) {
	t.Parallel()
	if got := (Header{Tags: []string{" "}}).Text(); got != "" {
		t.Fatalf("Text() = %q, want empty", got)
	}
}

func TestHeaderTextIsCapped(t *testing.T) {
	t.Parallel()
	h := Header{Summary: strings.Repeat("лишній текст ", 1000)}

	if n := utf8.RuneCountInString(h.Text()); n > HeaderMaxRunes+1 {
		t.Fatalf("header is %d runes, over the %d cap", n, HeaderMaxRunes)
	}
}
