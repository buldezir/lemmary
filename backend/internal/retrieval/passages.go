package retrieval

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"lemmary/backend/internal/strutil"
)

const (
	// Three short verbatim passages is where a hit stops being a filename and
	// starts being evidence, without the result list turning into a read.
	MaxPassagesPerDocument = 3

	// Below the floor a passage cannot carry a figure and its label; above the
	// ceiling one document crowds out the rest.
	minPassageBudget = 240
	maxPassageBudget = 1500
)

// Page is 0 while no OCR provider preserves page boundaries.
type Passage struct {
	Page      int
	StartByte int
	EndByte   int
	Text      string
}

// SelectPassages fuses the dense and lexical chunk lists for one document and
// keeps the best few. Either list may be empty.
//
// Offsets that no longer fit ocrText are dropped rather than clamped: they
// chunk an older revision of the text, and a clamped slice of the current one
// is a quote from nowhere.
func SelectPassages(ocrText string, dense, lexical []ChunkHit, budgetBytes int) []Passage {
	if budgetBytes <= 0 {
		return nil
	}

	byKey := map[string]ChunkHit{}
	rank := func(hits []ChunkHit) []Ranked {
		out := make([]Ranked, 0, len(hits))
		for _, hit := range hits {
			key := hit.DocumentID + "\x00" + strconv.Itoa(hit.Ord)
			// First list to mention a chunk owns its text: dense is passed
			// first with stored chunk text, lexical may carry only a fragment.
			if _, ok := byKey[key]; !ok {
				byKey[key] = hit
			}
			out = append(out, Ranked{ID: key, Score: hit.Score})
		}
		return out
	}

	fused := RRF(rank(dense), rank(lexical))
	if len(fused) == 0 {
		return nil
	}

	// Resolve first, then spend: a chunk with stale offsets must not eat a slot.
	texts := make([]ChunkHit, 0, MaxPassagesPerDocument)
	for _, item := range fused {
		if len(texts) == MaxPassagesPerDocument {
			break
		}
		hit := byKey[item.ID]
		text := passageText(ocrText, hit)
		if text == "" {
			continue
		}
		hit.Text = text
		texts = append(texts, hit)
	}
	if len(texts) == 0 {
		return nil
	}

	per := budgetBytes / len(texts)
	if per < minPassageBudget {
		per = minPassageBudget
	}

	passages := make([]Passage, 0, len(texts))
	spent := 0
	for _, hit := range texts {
		left := budgetBytes - spent
		if left <= 0 {
			break
		}
		limit := per
		if limit > left {
			limit = left
		}
		text := hit.Text
		if len(text) > limit {
			// The ellipsis is part of what is spent.
			text = strings.TrimSpace(strutil.Truncate(text, limit-len(strutil.Ellipsis))) + strutil.Ellipsis
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		spent += len(text)
		passages = append(passages, Passage{
			Page:      hit.Page,
			StartByte: hit.StartByte,
			EndByte:   hit.EndByte,
			Text:      text,
		})
	}
	return passages
}

// The chunk's own text when it has one, else the slice its offsets point at.
func passageText(ocrText string, hit ChunkHit) string {
	if text := strings.TrimSpace(hit.Text); text != "" {
		return text
	}
	if hit.StartByte < 0 || hit.EndByte <= hit.StartByte || hit.EndByte > len(ocrText) {
		return ""
	}
	start, end := hit.StartByte, hit.EndByte
	for start > 0 && start < len(ocrText) && !utf8.RuneStart(ocrText[start]) {
		start--
	}
	for end < len(ocrText) && !utf8.RuneStart(ocrText[end]) {
		end++
	}
	return strings.TrimSpace(ocrText[start:end])
}

// Kept either side of a matched term: enough for the sentence around a figure.
const snippetContextBytes = 200

// LexicalChunks centres passage-sized candidates on where the query's terms
// actually occur, so each carries real offsets back into the document. Terms
// that do not literally occur produce nothing, and the caller falls back to the
// index's own highlight.
func LexicalChunks(documentID, ocrText, query string, max int) []ChunkHit {
	if max <= 0 || ocrText == "" {
		return nil
	}
	terms := focusTerms(query)
	if len(terms) == 0 {
		return nil
	}

	windows := Windows(ocrText, nil)
	ranked := TermOverlap(ocrText, windows, query)
	byOrd := map[string]Window{}
	for _, w := range windows {
		byOrd[strconv.Itoa(w.Ord)] = w
	}

	hits := make([]ChunkHit, 0, max)
	for _, item := range ranked {
		if len(hits) == max {
			break
		}
		w, ok := byOrd[item.ID]
		if !ok {
			continue
		}
		start, end := narrowToMatch(ocrText, w, terms)
		if end <= start {
			continue
		}
		hits = append(hits, ChunkHit{
			DocumentID: documentID,
			Ord:        w.Ord,
			Page:       w.Page,
			Score:      item.Score,
			StartByte:  start,
			EndByte:    end,
			Text:       strings.TrimSpace(ocrText[start:end]),
		})
	}
	return hits
}

// Narrow shrinks each chunk hit to the text around the query's terms: a chunk
// is sized for embedding, not quoting, so a budget paying for a third of one
// would otherwise quote its opening. A hit whose terms cannot be located is
// passed through as it came, because a coarse quote is still evidence.
func Narrow(ocrText, query string, hits []ChunkHit) []ChunkHit {
	if len(hits) == 0 {
		return nil
	}
	terms := focusTerms(query)
	out := make([]ChunkHit, 0, len(hits))
	for _, hit := range hits {
		narrowed := hit
		if len(terms) > 0 {
			w := Window{Ord: hit.Ord, Page: hit.Page, StartByte: hit.StartByte, EndByte: hit.EndByte}
			if start, end := narrowToMatch(ocrText, w, terms); end > start {
				narrowed.StartByte = start
				narrowed.EndByte = end
				narrowed.Text = strings.TrimSpace(ocrText[start:end])
			}
		}
		out = append(out, narrowed)
	}
	return out
}

// Shrinks a window around its first matched term, so a passage quotes the match
// rather than whatever happened to start the window.
func narrowToMatch(ocrText string, w Window, terms []string) (int, int) {
	if w.StartByte < 0 || w.EndByte <= w.StartByte || w.EndByte > len(ocrText) {
		return 0, 0
	}
	lower := strings.ToLower(ocrText[w.StartByte:w.EndByte])
	at, width := -1, 0
	for _, term := range terms {
		i := strings.Index(lower, term)
		if i >= 0 && (at < 0 || i < at) {
			at, width = i, len(term)
		}
	}
	if at < 0 {
		return 0, 0
	}
	start := alignForward(ocrText, w.StartByte+at-snippetContextBytes)
	if start < w.StartByte {
		start = w.StartByte
	}
	end := alignForward(ocrText, w.StartByte+at+width+snippetContextBytes)
	if end > w.EndByte {
		end = w.EndByte
	}
	return start, end
}

// Clamped so neither a long result list nor a single hit makes quotes useless.
func PassageBudgetPerDoc(capBytes, docs int) int {
	if docs <= 0 || capBytes <= 0 {
		return 0
	}
	per := capBytes / docs
	if per < minPassageBudget {
		per = minPassageBudget
	}
	if per > maxPassageBudget {
		per = maxPassageBudget
	}
	return per
}
