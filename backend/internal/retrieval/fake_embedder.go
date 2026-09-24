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

// HashEmbedder is a deterministic stand-in for a real embedding provider: word
// and n-gram hashes, L2-normalised, so cosine behaves as it does for a real
// model. Not a semantic model, and never will link "car insurance" to
// "Kfz-Versicherung"; it only gives the tests a dense signal genuinely
// different from token-exact BM25, with no provider and no API key.
type HashEmbedder struct {
	// 0 means DefaultHashDim.
	Dim int
}

// Small enough to keep the eval fast, large enough that unrelated texts do not
// collide into looking similar.
const DefaultHashDim = 256

// Four is short enough to survive a German compound and long enough not to
// match everything.
const NGramSize = 4

func (h HashEmbedder) dim() int {
	if h.Dim > 0 {
		return h.Dim
	}
	return DefaultHashDim
}

func (h HashEmbedder) Dims() int { return h.dim() }

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

// Vectors of different lengths score 0 rather than panicking: a dims mismatch
// is a configuration bug, not a ranking question.
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

// MemoryChunks is a ChunkSearcher over a slice: cosine kNN, term overlap, and
// RRF when both are given, the same shape as the real chunk index.
type MemoryChunks struct {
	Chunks []MemoryChunk
}

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

func (m *MemoryChunks) SearchChunks(_ context.Context, q ChunkQuery) ([]ChunkHit, error) {
	if m == nil {
		return nil, nil
	}
	eligible := map[string]struct{}{}
	for _, id := range q.DocumentIDs {
		eligible[id] = struct{}{}
	}
	shared := map[string]struct{}{}
	for _, id := range q.SharedDocumentIDs {
		shared[id] = struct{}{}
	}

	byKey := map[string]ChunkHit{}
	dense := make([]Ranked, 0)
	lexical := make([]Ranked, 0)
	terms := focusTerms(q.Text)

	for _, chunk := range m.Chunks {
		if _, ok := shared[chunk.DocumentID]; q.UserID != "" && chunk.UserID != q.UserID && !ok {
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
