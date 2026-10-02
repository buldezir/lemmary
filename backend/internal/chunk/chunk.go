// Package chunk cuts a document's OCR text into overlapping passages.
//
// The cuts must be reproducible from the text alone: a chunk is stored as a
// byte range into documents.ocr_text, so re-running the chunker on unchanged
// text has to produce byte-identical ranges or a stored vector starts
// describing a different passage. Version guards that: bump it whenever the cut
// rules change, and every document re-chunks and re-embeds.
package chunk

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Stored per document; a mismatch makes the document a backfill candidate.
// 2 stopped counting padding (see Compact) towards a chunk's size.
const Version = 2

// A half-open byte range [Start, End) into the text it was cut from.
type Chunk struct {
	Start int
	End   int
}

// Sizes are in runes, not bytes: the budget being spent is the model's token
// window, which tracks characters far better than UTF-8 bytes. Only runes that
// survive Compact count, so a padded OCR table yields chunks of content rather
// than chunks of spaces.
type Options struct {
	TargetRunes int
	// A hard ceiling, even with no whitespace to cut at.
	MaxRunes int
	// How far in cut candidates start being considered, so one early newline
	// cannot produce a two-word chunk.
	MinRunes int
	// How far the next chunk backs up, so a sentence spanning a cut is still
	// whole in one of the two.
	OverlapRunes int
	// Past this the tail is dropped and the caller is told: a 20 MB OCR column
	// would otherwise be tens of thousands of embedding calls.
	MaxChunks int
}

// ~1100 runes is roughly 250-350 tokens of Latin text: inside every embedding
// model's window, small enough that a hit points at a paragraph not a page.
func DefaultOptions() Options {
	return Options{
		TargetRunes:  1100,
		MaxRunes:     1400,
		MinRunes:     600,
		OverlapRunes: 150,
		MaxChunks:    3000,
	}
}

// Repairs an Options built by hand, so a zero value is usable and an
// inconsistent one cannot loop forever.
func (o Options) normalize() Options {
	d := DefaultOptions()
	if o.TargetRunes <= 0 {
		o.TargetRunes = d.TargetRunes
	}
	if o.MaxRunes <= 0 {
		o.MaxRunes = d.MaxRunes
	}
	if o.MaxRunes < o.TargetRunes {
		o.MaxRunes = o.TargetRunes
	}
	if o.MinRunes <= 0 || o.MinRunes >= o.MaxRunes {
		o.MinRunes = min(d.MinRunes, o.MaxRunes-1)
	}
	if o.MinRunes < 1 {
		o.MinRunes = 1
	}
	if o.OverlapRunes < 0 {
		o.OverlapRunes = 0
	}
	// An overlap at or past MinRunes would let the cursor stand still.
	if o.OverlapRunes >= o.MinRunes {
		o.OverlapRunes = o.MinRunes - 1
	}
	if o.MaxChunks <= 0 {
		o.MaxChunks = d.MaxChunks
	}
	return o
}

// truncated is true when MaxChunks stopped before the end of the text, which
// the caller records so the gap does not look like a complete document.
// Whitespace-only text yields no chunks: a vector of nothing pollutes kNN.
func Split(text string, opts Options) (chunks []Chunk, truncated bool) {
	opts = opts.normalize()
	if strings.TrimSpace(text) == "" {
		return nil, false
	}

	start := 0
	for start < len(text) {
		maxEnd, count := advance(text, start, opts.MaxRunes)
		if count <= opts.MaxRunes && maxEnd == len(text) {
			chunks = append(chunks, Chunk{Start: start, End: len(text)})
			break
		}

		minEnd, _ := advance(text, start, opts.MinRunes)
		targetEnd, _ := advance(text, start, opts.TargetRunes)
		end := findCut(text, minEnd, targetEnd, maxEnd)
		chunks = append(chunks, Chunk{Start: start, End: end})

		if len(chunks) >= opts.MaxChunks {
			truncated = end < len(text)
			break
		}

		next := backUp(text, start, end, opts.OverlapRunes)
		if next <= start {
			// The cursor must advance or the loop cannot terminate.
			next = end
		}
		start = next
	}
	return chunks, truncated
}

// Compact is what gets embedded in place of a chunk's raw text: OCR tables pad
// cells with spaces and rules with dashes, and a passage that is mostly padding
// embeds as a vector of nothing that sits near every short query.
func Compact(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := range len(text) {
		if !padding(text, i, 0) {
			b.WriteByte(text[i])
		}
	}
	return b.String()
}

// Whether the byte at i only lengthens a run Compact shortens: a second space
// or tab, or a fourth repeat of an ASCII punctuation mark. Runs are counted
// from floor. Both are ASCII, so a byte test never splits a rune.
func padding(text string, i, floor int) bool {
	c := text[i]
	switch {
	case c == ' ' || c == '\t':
		return i > floor && (text[i-1] == ' ' || text[i-1] == '\t')
	case c < utf8.RuneSelf && (unicode.IsPunct(rune(c)) || unicode.IsSymbol(rune(c))):
		return i-3 >= floor && text[i-3] == c && text[i-2] == c && text[i-1] == c
	}
	return false
}

// Returns the byte offset reached and how many content runes it covered (fewer
// at end of text). Padding right after the last one is taken along, so a text
// ending in padding ends in this chunk.
func advance(text string, from, n int) (int, int) {
	i := from
	count := 0
	for count < n && i < len(text) {
		_, size := utf8.DecodeRuneInString(text[i:])
		if !padding(text, i, from) {
			count++
		}
		i += size
	}
	for i < len(text) && padding(text, i, from) {
		i++
	}
	return i, count
}

// Cut priorities, best first. A bare rune boundary is the last resort, for
// text with no whitespace at all (a base64 blob, a CJK run).
const (
	cutParagraph = iota
	cutNewline
	cutSentence
	cutWhitespace
	cutTiers
)

// Within the best available tier it takes the candidate closest to targetEnd,
// ties to the later one, so chunks stay near the target rather than the maximum.
func findCut(text string, minEnd, targetEnd, maxEnd int) int {
	best := [cutTiers]int{-1, -1, -1, -1}

	for i := minEnd; i < maxEnd; {
		r, size := utf8.DecodeRuneInString(text[i:])
		next := i + size

		switch {
		case r == '\n' && strings.HasPrefix(text[next:], "\n"):
			consider(&best[cutParagraph], next+1, targetEnd, maxEnd)
		case r == '\n':
			consider(&best[cutNewline], next, targetEnd, maxEnd)
		case isSentenceEnd(r) && followedBySpace(text, next):
			consider(&best[cutSentence], next, targetEnd, maxEnd)
		case unicode.IsSpace(r):
			consider(&best[cutWhitespace], next, targetEnd, maxEnd)
		}
		i = next
	}

	for _, candidate := range best {
		if candidate > 0 {
			return candidate
		}
	}
	return maxEnd
}

func consider(best *int, candidate, targetEnd, maxEnd int) {
	if candidate > maxEnd {
		return
	}
	if *best < 0 {
		*best = candidate
		return
	}
	d := abs(candidate - targetEnd)
	bd := abs(*best - targetEnd)
	if d < bd || (d == bd && candidate > *best) {
		*best = candidate
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func isSentenceEnd(r rune) bool {
	switch r {
	case '.', '!', '?', '…', '。', '！', '？':
		return true
	default:
		return false
	}
}

func followedBySpace(text string, at int) bool {
	if at >= len(text) {
		return true
	}
	r, _ := utf8.DecodeRuneInString(text[at:])
	return unicode.IsSpace(r)
}

// The start of the next chunk: overlap runes before end, moved forward to a
// whitespace boundary. Never returns an offset at or before floor.
func backUp(text string, floor, end, overlap int) int {
	if overlap <= 0 {
		return end
	}

	i := end
	count := 0
	for count < overlap && i > floor {
		_, size := utf8.DecodeLastRuneInString(text[:i])
		i -= size
		if !padding(text, i, floor) {
			count++
		}
	}
	if i <= floor {
		return end
	}

	// Bounded by end, so this can only shorten the overlap.
	for j := i; j < end; {
		r, size := utf8.DecodeRuneInString(text[j:])
		if unicode.IsSpace(r) {
			return j + size
		}
		j += size
	}
	return i
}
