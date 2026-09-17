package appapi

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/retrieval"
)

// A chunk ordinal is the address the helper and the research model share: the
// same ocr_text splits the same way every time, so "chunk 12" names the same
// bytes whoever asks.
func splitChunks(text string) []chunk.Chunk {
	chunks, _ := chunk.Split(text, chunk.DefaultOptions())
	return chunks
}

// markChunks prefixes each chunk's first byte with its ordinal, so the helper
// can point at what it read. Overlaps stay: a marker is a position, not a cut.
func markChunks(text string) string {
	chunks := splitChunks(text)
	if len(chunks) == 0 {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + 12*len(chunks))
	prev := 0
	for i, c := range chunks {
		b.WriteString(text[prev:c.Start])
		fmt.Fprintf(&b, "[chunk %d]\n", i)
		prev = c.Start
	}
	b.WriteString(text[prev:])
	return b.String()
}

// sliceChunks returns the text of the given ordinals in document order,
// consecutive ones as a single run so the overlap is not repeated, each run
// headed by its range. Ordinals outside the document are returned separately.
func sliceChunks(text string, ords []int) (out string, total int, unknown []int) {
	chunks := splitChunks(text)
	total = len(chunks)
	valid := make([]int, 0, len(ords))
	seen := map[int]struct{}{}
	for _, ord := range ords {
		if _, dup := seen[ord]; dup {
			continue
		}
		seen[ord] = struct{}{}
		if ord < 0 || ord >= total {
			unknown = append(unknown, ord)
			continue
		}
		valid = append(valid, ord)
	}
	sort.Ints(valid)
	sort.Ints(unknown)

	var b strings.Builder
	for i := 0; i < len(valid); {
		j := i
		for j+1 < len(valid) && valid[j+1] == valid[j]+1 {
			j++
		}
		if b.Len() > 0 {
			b.WriteString("\n\n…\n\n")
		}
		if i == j {
			fmt.Fprintf(&b, "[chunk %d]\n", valid[i])
		} else {
			fmt.Fprintf(&b, "[chunk %d-%d]\n", valid[i], valid[j])
		}
		b.WriteString(text[chunks[valid[i]].Start:chunks[valid[j]].End])
		i = j + 1
	}
	return b.String(), total, unknown
}

// chunkExcerpt is what the helper is shown of one candidate: the whole text
// when it fits, else the chunks that best match the question, in document
// order, marked with real ordinals. rank may be nil.
func chunkExcerpt(documentID, text, question string, rank focusRanker, budget int) (string, int) {
	chunks := splitChunks(text)
	if len(text) <= budget {
		return markChunks(text), len(chunks)
	}
	windows := make([]retrieval.Window, 0, len(chunks))
	for i, c := range chunks {
		windows = append(windows, retrieval.Window{Ord: i, StartByte: c.Start, EndByte: c.End})
	}
	var ranked []retrieval.Ranked
	if rank != nil {
		_, ranked = rank(documentID, text, question)
	}
	if len(ranked) == 0 {
		ranked = retrieval.TermOverlap(text, windows, question)
	}
	keep := make([]int, 0)
	spent := 0
	for _, r := range ranked {
		ord, err := strconv.Atoi(r.ID)
		if err != nil || ord < 0 || ord >= len(chunks) {
			continue
		}
		size := chunks[ord].End - chunks[ord].Start
		if spent+size > budget {
			break
		}
		keep = append(keep, ord)
		spent += size
	}
	if len(keep) == 0 && len(chunks) > 0 {
		keep = append(keep, 0)
	}
	out, _, _ := sliceChunks(text, keep)
	return out, len(chunks)
}
