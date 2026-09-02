package retrieval

import (
	"context"
	"hash/fnv"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// HashEmbedder is a deterministic stand-in for a real embedding provider: it
// hashes a text's words and their character n-grams into a fixed number of
// buckets and L2-normalises the result, so cosine similarity behaves the way it
// does for a real model — related texts score higher than unrelated ones, and
// the score is bounded.
//
// It is not a semantic model and cannot be: it will never link "car insurance"
// to "Kfz-Versicherung". What it does give the tests is a dense signal that is
// genuinely different from token-exact BM25 — the n-grams make it robust to
// typos and to morphology — so the fusion code can be exercised end to end with
// no provider, no API key, and no build tag.
type HashEmbedder struct {
	// Dim is the vector length; 0 means DefaultHashDim.
	Dim int
}

// DefaultHashDim is small enough to keep the eval fast and large enough that
// unrelated texts do not collide into looking similar.
const DefaultHashDim = 256

// NGramSize is the character n-gram width HashEmbedder adds on top of whole
// words. Four is short enough to survive a German compound and long enough not
// to match everything.
const NGramSize = 4

func (h HashEmbedder) dim() int {
	if h.Dim > 0 {
		return h.Dim
	}
	return DefaultHashDim
}

// Dims reports the vector length this embedder produces.
func (h HashEmbedder) Dims() int { return h.dim() }

// Embed hashes each input into a unit vector.
func (h HashEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	out := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		out = append(out, h.embedOne(input))
	}
	return out, nil
}

func (h HashEmbedder) embedOne(text string) []float32 {
	dim := h.dim()
	vec := make([]float32, dim)
	for _, term := range focusTerms(text) {
		addHashed(vec, term, 1)
		if utf8.RuneCountInString(term) > NGramSize {
			runes := []rune(term)
			for i := 0; i+NGramSize <= len(runes); i++ {
				addHashed(vec, string(runes[i:i+NGramSize]), 0.5)
			}
		}
	}
	norm := float32(0)
	for _, v := range vec {
		norm += v * v
	}
	if norm == 0 {
		return vec
	}
	norm = float32(math.Sqrt(float64(norm)))
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}

func addHashed(vec []float32, token string, weight float32) {
	sum := fnv.New32a()
	_, _ = sum.Write([]byte(token))
	bucket := int(sum.Sum32() % uint32(len(vec)))
	// A second hash decides the sign, so unrelated tokens landing in the same
	// bucket cancel as often as they add rather than always adding.
	sign := float32(1)
	if sum.Sum32()&0x10000 != 0 {
		sign = -1
	}
	vec[bucket] += sign * weight
}

// Cosine is the similarity two unit vectors of equal length have. Vectors of
// different lengths score 0 rather than panicking: a dims mismatch is a
// configuration bug, not a ranking question.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// MemoryChunk is one indexed chunk of the in-memory ChunkSearcher.
type MemoryChunk struct {
	DocumentID string
	UserID     string
	Ord        int
	Page       int
	StartByte  int
	EndByte    int
	Text       string
	Vector     []float32
}

// MemoryChunks is a ChunkSearcher over a slice: cosine kNN for the vector,
// term overlap for the text, fused with RRF when both are given — the same
// shape as the real chunk index, without the index.
type MemoryChunks struct {
	Chunks []MemoryChunk
}

// NewMemoryChunks embeds every chunk's text and returns a searcher over them.
func NewMemoryChunks(ctx context.Context, embedder Embedder, chunks []MemoryChunk) (*MemoryChunks, error) {
	texts := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		texts = append(texts, chunk.Text)
	}
	vectors, err := embedder.Embed(ctx, texts)
	if err != nil {
		return nil, err
	}
	stored := make([]MemoryChunk, len(chunks))
	copy(stored, chunks)
	for i := range stored {
		if i < len(vectors) {
			stored[i].Vector = vectors[i]
		}
	}
	return &MemoryChunks{Chunks: stored}, nil
}

// SearchChunks implements ChunkSearcher.
func (m *MemoryChunks) SearchChunks(_ context.Context, q ChunkQuery) ([]ChunkHit, error) {
	if m == nil {
		return nil, nil
	}
	eligible := map[string]struct{}{}
	for _, id := range q.DocumentIDs {
		eligible[id] = struct{}{}
	}

	byKey := map[string]ChunkHit{}
	dense := make([]Ranked, 0)
	lexical := make([]Ranked, 0)
	terms := focusTerms(q.Text)

	for _, chunk := range m.Chunks {
		if q.UserID != "" && chunk.UserID != q.UserID {
			continue
		}
		if len(eligible) > 0 {
			if _, ok := eligible[chunk.DocumentID]; !ok {
				continue
			}
		}
		key := chunk.DocumentID + "\x00" + strconv.Itoa(chunk.Ord)
		byKey[key] = ChunkHit{
			DocumentID: chunk.DocumentID,
			Ord:        chunk.Ord,
			Page:       chunk.Page,
			StartByte:  chunk.StartByte,
			EndByte:    chunk.EndByte,
			Text:       chunk.Text,
		}
		if len(q.Vector) > 0 {
			if score := Cosine(q.Vector, chunk.Vector); score > 0 {
				dense = append(dense, Ranked{ID: key, Score: score})
			}
		}
		if len(terms) > 0 {
			lower := strings.ToLower(chunk.Text)
			matched := 0
			for _, term := range terms {
				if strings.Contains(lower, term) {
					matched++
				}
			}
			if matched > 0 {
				lexical = append(lexical, Ranked{ID: key, Score: float64(matched)})
			}
		}
	}

	sortRanked := func(list []Ranked) {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].Score != list[j].Score {
				return list[i].Score > list[j].Score
			}
			return list[i].ID < list[j].ID
		})
	}
	sortRanked(dense)
	sortRanked(lexical)

	var fused []Ranked
	switch {
	case len(dense) > 0 && len(lexical) > 0:
		fused = RRF(dense, lexical)
	case len(dense) > 0:
		fused = dense
	default:
		fused = lexical
	}

	k := q.K
	if k <= 0 || k > len(fused) {
		k = len(fused)
	}
	hits := make([]ChunkHit, 0, k)
	for _, item := range fused[:k] {
		hit := byKey[item.ID]
		hit.Score = item.Score
		hits = append(hits, hit)
	}
	return hits, nil
}
