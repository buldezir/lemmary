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
const Version = 1

// A half-open byte range [Start, End) into the text it was cut from.
type Chunk struct {
	Start int
	End   int
}

// Sizes are in runes, not bytes: the budget being spent is the model's token
// window, which tracks characters far better than UTF-8 bytes.
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

// Returns the byte offset reached and how many runes it covered (fewer at end
// of text).
func advance(text string, from, n int) (int, int) {
	i := from
	count := 0
	for count < n && i < len(text) {
		_, size := utf8.DecodeRuneInString(text[i:])
		i += size
		count++
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
		count++
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
