package retrieval

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// Roughly a chunk: big enough to carry a figure with its labelling sentence.
	targetWindowBytes = 1000
	// Bounds the search for a break past the target, so a document with no
	// break at all still makes progress.
	maxWindowBytes = 1600

	// Always shown, whatever the focus matched: the head carries letterheads
	// and dates, the tail signatures, sums and payment terms.
	excerptHeadBytes = 1500
	excerptTailBytes = 500

	// What one "…\n[offset N]" gap marker costs, held back from the budget so
	// an excerpt cannot overshoot it.
	markerOverhead = 32
)

type Window struct {
	Ord       int
	Page      int
	StartByte int
	EndByte   int
}

// Windows returns the regions a focused read may quote from. Stored chunk
// boundaries are preferred, being what was embedded; those that no longer fit
// the text are dropped, and if none survive the windows are derived from the
// text, so a focused read never fails just because chunks are stale.
func Windows(ocrText string, stored []Window) []Window {
	if len(ocrText) == 0 {
		return nil
	}

	if len(stored) > 0 {
		out := make([]Window, 0, len(stored))
		for _, w := range stored {
			if w.StartByte < 0 || w.EndByte <= w.StartByte || w.EndByte > len(ocrText) {
				continue
			}
			out = append(out, w)
		}
		if len(out) > 0 {
			sort.SliceStable(out, func(i, j int) bool { return out[i].StartByte < out[j].StartByte })
			return out
		}
	}

	windows := make([]Window, 0, len(ocrText)/targetWindowBytes+1)
	ord := 0
	for cursor := 0; cursor < len(ocrText); {
		end := windowEnd(ocrText, cursor)
		windows = append(windows, Window{Ord: ord, StartByte: cursor, EndByte: end})
		ord++
		cursor = end
	}
	return windows
}

// The next break at or after the target size: paragraph, then line, then word.
// It always advances.
func windowEnd(text string, start int) int {
	ideal := start + targetWindowBytes
	if ideal >= len(text) {
		return len(text)
	}
	max := start + maxWindowBytes
	if max >= len(text) {
		return len(text)
	}
	if i := strings.Index(text[ideal:max], "\n\n"); i >= 0 {
		return ideal + i + 2
	}
	if i := strings.IndexByte(text[ideal:max], '\n'); i >= 0 {
		return ideal + i + 1
	}
	if i := strings.IndexByte(text[ideal:max], ' '); i >= 0 {
		return ideal + i + 1
	}
	// No break at all (CJK, or a table dumped without spaces): cut on a rune
	// boundary rather than not at all.
	end := max
	for end > start && !utf8.RuneStart(text[end]) {
		end--
	}
	if end <= start {
		return max
	}
	return end
}

// TermOverlap ranks windows by how much of focus they contain, the fallback
// when there is no dense ranking. Terms match as substrings, not tokens, which
// survives morphology: "Rechnung" finds "Rechnungsbetrag" without a stemmer,
// and the archive has no stemmer for half its languages.
func TermOverlap(ocrText string, windows []Window, focus string) []Ranked {
	terms := focusTerms(focus)
	if len(terms) == 0 || len(windows) == 0 {
		return nil
	}

	scored := make([]Ranked, 0, len(windows))
	for _, w := range windows {
		if w.StartByte < 0 || w.EndByte <= w.StartByte || w.EndByte > len(ocrText) {
			continue
		}
		text := strings.ToLower(ocrText[w.StartByte:w.EndByte])
		distinct := 0
		occurrences := 0
		for _, term := range terms {
			n := strings.Count(text, term)
			if n == 0 {
				continue
			}
			distinct++
			occurrences += n
		}
		if distinct == 0 {
			continue
		}
		// Covering more of the question beats repeating one word of it.
		extra := min(occurrences-distinct, 10)
		scored = append(scored, Ranked{
			ID:    strconv.Itoa(w.Ord),
			Score: float64(distinct) + 0.05*float64(extra),
		})
	}

	sort.SliceStable(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	return scored
}

func focusTerms(focus string) []string {
	fields := strings.FieldsFunc(strings.ToLower(focus), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := make([]string, 0, len(fields))
	seen := map[string]struct{}{}
	for _, field := range fields {
		if utf8.RuneCountInString(field) < 2 {
			continue
		}
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		terms = append(terms, field)
	}
	return terms
}

type span struct{ start, end int }

// Excerpt renders a long document as its head, the best-ranked windows in
// document order, and its tail, with "…" and a byte offset marking every gap.
// It returns how many ranked windows did not fit, so the model can be told
// there is more and ask for it by offset.
func Excerpt(ocrText string, windows []Window, ranked []Ranked, budgetBytes int) (string, int) {
	if budgetBytes <= 0 || ocrText == "" {
		return "", 0
	}
	if len(ocrText) <= budgetBytes {
		return ocrText, 0
	}

	// There is always at least one gap marker to pay for.
	usable := budgetBytes - markerOverhead
	if usable < 1 {
		usable = budgetBytes
	}
	headLen, tailLen := excerptHeadBytes, excerptTailBytes
	if headLen+tailLen > usable {
		headLen = usable * 3 / 4
		tailLen = usable - headLen
	}
	// Backwards for the head, forwards for the tail: rune alignment then only
	// shrinks a segment, so neither can overrun what was reserved.
	headEnd := alignBack(ocrText, headLen)
	tailStart := max(alignForward(ocrText, len(ocrText)-tailLen), headEnd)

	spans := []span{{0, headEnd}}
	if tailStart < len(ocrText) {
		spans = append(spans, span{tailStart, len(ocrText)})
	}

	byOrd := map[string]Window{}
	for _, w := range windows {
		byOrd[strconv.Itoa(w.Ord)] = w
	}

	remaining := usable - headEnd - (len(ocrText) - tailStart)
	omitted := 0
	for _, item := range ranked {
		w, ok := byOrd[item.ID]
		if !ok {
			continue
		}
		start, end := alignForward(ocrText, w.StartByte), alignForward(ocrText, w.EndByte)
		if start < headEnd {
			start = headEnd
		}
		if end > tailStart {
			end = tailStart
		}
		if end <= start {
			// Already inside the head or the tail: shown, not omitted.
			continue
		}
		cost := end - start + markerOverhead
		if cost > remaining {
			omitted++
			continue
		}
		remaining -= cost
		spans = append(spans, span{start, end})
	}

	sort.SliceStable(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	merged := make([]span, 0, len(spans))
	for _, s := range spans {
		if n := len(merged); n > 0 && s.start <= merged[n-1].end {
			if s.end > merged[n-1].end {
				merged[n-1].end = s.end
			}
			continue
		}
		merged = append(merged, s)
	}

	var b strings.Builder
	for i, s := range merged {
		if i > 0 {
			fmt.Fprintf(&b, "\n\n…\n[offset %d]\n", s.start)
		}
		b.WriteString(ocrText[s.start:s.end])
	}
	return b.String(), omitted
}

func alignBack(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return i
}

func alignForward(s string, i int) int {
	if i <= 0 {
		return 0
	}
	if i >= len(s) {
		return len(s)
	}
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return i
}
