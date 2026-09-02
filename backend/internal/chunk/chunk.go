// Package chunk cuts a document's OCR text into overlapping passages.
//
// The cuts have to be reproducible from the text alone: a chunk is stored as a
// byte range into documents.ocr_text, and the passage a reader is shown is that
// range sliced out of the live column. Re-running the chunker on unchanged text
// must therefore produce byte-identical ranges, or a stored vector would start
// describing a different passage than the one it was built from. That is what
// Version guards -- bump it whenever the cut rules change, and every document
// re-chunks and re-embeds.
package chunk

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Version identifies the cut rules. Stored per document; a mismatch makes the
// document a backfill candidate.
const Version = 1

// Chunk is a half-open byte range [Start, End) into the text it was cut from.
type Chunk struct {
	Start int
	End   int
}

// Options are the chunk sizes, in runes. Runes rather than bytes because the
// budget being spent is the embedding model's token window, which tracks
// characters far better than it tracks UTF-8 bytes -- a Cyrillic or CJK
// document is twice the bytes of an English one for the same amount of text.
type Options struct {
	// TargetRunes is the size a cut aims for.
	TargetRunes int
	// MaxRunes is the hard ceiling; a chunk never exceeds it, even with no
	// whitespace to cut at.
	MaxRunes int
	// MinRunes is how far into a chunk cut candidates start being considered,
	// so one early newline cannot produce a two-word chunk.
	MinRunes int
	// OverlapRunes is how far the next chunk backs up, so a sentence spanning a
	// cut is still whole in one of the two.
	OverlapRunes int
	// MaxChunks bounds one document. A 20 MB OCR column would otherwise be tens
	// of thousands of embedding calls; past this the tail is dropped and the
	// caller is told.
	MaxChunks int
}

// DefaultOptions is what the pipeline uses. ~1100 runes is roughly 250-350
// tokens for Latin text, comfortably inside every embedding model's window and
// small enough that a hit points at a paragraph rather than a page.
func DefaultOptions() Options {
	return Options{
		TargetRunes:  1100,
		MaxRunes:     1400,
		MinRunes:     600,
		OverlapRunes: 150,
		MaxChunks:    3000,
	}
}

// normalize repairs an Options a caller built by hand, so a zero value is
// usable and an inconsistent one cannot loop forever.
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
	// An overlap at or past the minimum chunk size would let the cursor stand
	// still: the next chunk would begin at or before the previous one did.
	if o.OverlapRunes >= o.MinRunes {
		o.OverlapRunes = o.MinRunes - 1
	}
	if o.MaxChunks <= 0 {
		o.MaxChunks = d.MaxChunks
	}
	return o
}

// Split cuts text into chunks. truncated is true when MaxChunks stopped it
// before the end of the text, which the caller records so the gap is visible
// rather than looking like a complete document.
//
// Text that is entirely whitespace yields no chunks: there is nothing to embed,
// and a vector of nothing pollutes every kNN result.
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
			// The cursor must advance or the loop cannot terminate; giving up
			// the overlap is the cheaper of the two failures.
			next = end
		}
		start = next
	}
	return chunks, truncated
}

// advance walks n runes forward from the byte offset from, returning the byte
// offset reached and how many runes it actually covered (fewer at end of text).
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

// Cut priorities, best first. A paragraph break is the strongest signal that
// two passages are about different things; a bare rune boundary is the last
// resort for text with no whitespace at all (a base64 blob, a CJK run).
const (
	cutParagraph = iota
	cutNewline
	cutSentence
	cutWhitespace
	cutTiers
)

// findCut picks the chunk end inside [minEnd, maxEnd]. Within the best
// available tier it takes the candidate closest to targetEnd, ties going to the
// later one, so chunks stay near the target size instead of always stretching
// to the maximum.
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

// backUp returns the start of the next chunk: overlap runes before end, moved
// forward to the next whitespace boundary so the overlap begins at a word.
// Never returns an offset at or before floor.
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

	// Slide forward to the first boundary after a space, so the overlap does
	// not start mid-word. Bounded by end, so this can only shorten the overlap.
	for j := i; j < end; {
		r, size := utf8.DecodeRuneInString(text[j:])
		if unicode.IsSpace(r) {
			return j + size
		}
		j += size
	}
	return i
}
