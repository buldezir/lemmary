package fulltext

import (
	"fmt"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/custom"
	"github.com/blevesearch/bleve/v2/analysis/token/lowercase"
	"github.com/blevesearch/bleve/v2/analysis/tokenizer/unicode"
	"github.com/blevesearch/bleve/v2/mapping"
	index "github.com/blevesearch/bleve_index_api"

	"lemmary/backend/internal/strutil"
)

// ChunkMappingVersion is bumped when the chunk mapping changes, exactly like
// MappingVersion. It is only the first field of the version file: the model and
// its dimensions are part of the same string, because a vector field's length
// is fixed at mapping time and vectors of another length are dropped silently.
const ChunkMappingVersion = "1"

// Chunk index fields. They are deliberately few: the chunk index answers
// "which passage, of whose document" and nothing else. Everything a result
// needs beyond that is read from SQLite by document id, so a tag rename never
// has to rewrite a vector.
const (
	FieldChunkDocumentID = "document_id"
	FieldChunkUser       = "user"
	FieldChunkOrd        = "ord"
	FieldChunkPage       = "page"
	FieldChunkStart      = "start_byte"
	FieldChunkEnd        = "end_byte"
	FieldChunkText       = "text"
	FieldChunkVector     = "vector"
)

// maxChunkTextRunes caps the copy of a passage the index stores. A chunk is
// ~1400 runes by construction, so this only bites on a header chunk that
// rendered a lot of metadata, and it bounds what one hit can cost.
const maxChunkTextRunes = 1200

// VectorSpec is the embedding binding the chunk index is built for: vectors of
// one length, produced by one model. Both halves matter — two models with the
// same dimension count produce vectors that mean nothing to each other — so
// both are written into the version file and a change to either wipes the
// index.
type VectorSpec struct {
	Model string
	Dims  int
}

// Valid reports whether the spec can back an index. Dims is 0 until the first
// embedding response comes back, which is the normal state of a fresh install
// with a model configured and nothing embedded yet.
func (s VectorSpec) Valid() bool {
	return strings.TrimSpace(s.Model) != "" &&
		s.Dims >= mapping.MinVectorDims && s.Dims <= mapping.MaxVectorDims
}

func (s VectorSpec) normalized() VectorSpec {
	return VectorSpec{Model: strings.TrimSpace(s.Model), Dims: s.Dims}
}

// version is the content of bleve/chunks.version.
func (s VectorSpec) version() string {
	return fmt.Sprintf("%s;model=%s;dims=%d", ChunkMappingVersion, s.Model, s.Dims)
}

// newChunkMapping builds the mapping for one VectorSpec.
//
// The analyzer is the same one the documents index uses, for the same reason
// (see newMapping): a chunk is a slice of the very text that index tokenizes,
// and two different analyzers over the same words would make the lexical half
// of a hybrid search disagree with itself.
func newChunkMapping(spec VectorSpec) (mapping.IndexMapping, error) {
	if !spec.Valid() {
		return nil, fmt.Errorf("chunk mapping needs a model and 1..%d dimensions, got %q/%d",
			mapping.MaxVectorDims, spec.Model, spec.Dims)
	}

	im := bleve.NewIndexMapping()
	im.DefaultAnalyzer = AnalyzerName
	im.DefaultField = FieldChunkText
	im.IndexDynamic = false
	im.StoreDynamic = false
	im.DocValuesDynamic = false
	im.ScoringModel = index.BM25Scoring

	if err := im.AddCustomAnalyzer(AnalyzerName, map[string]any{
		"type":          custom.Name,
		"tokenizer":     unicode.Name,
		"token_filters": []string{lowercase.Name},
	}); err != nil {
		return nil, err
	}

	doc := bleve.NewDocumentMapping()
	doc.Dynamic = false

	// Stored: a kNN hit carries no highlight fragments, so everything a
	// passage is made of has to come back from storage.
	doc.AddFieldMappingsAt(FieldChunkDocumentID, storedKeywordField())
	doc.AddFieldMappingsAt(FieldChunkUser, keywordField())
	doc.AddFieldMappingsAt(FieldChunkOrd, storedNumberField())
	doc.AddFieldMappingsAt(FieldChunkPage, storedNumberField())
	doc.AddFieldMappingsAt(FieldChunkStart, storedNumberField())
	doc.AddFieldMappingsAt(FieldChunkEnd, storedNumberField())
	doc.AddFieldMappingsAt(FieldChunkText, chunkTextField())
	doc.AddFieldMappingsAt(FieldChunkVector, vectorField(spec.Dims))

	im.DefaultMapping = doc
	return im, nil
}

func storedKeywordField() *mapping.FieldMapping {
	fm := bleve.NewKeywordFieldMapping()
	fm.IncludeInAll = false
	fm.IncludeTermVectors = false
	fm.Store = true
	return fm
}

// storedNumberField carries a number back with a hit without paying for a
// numeric range index nothing queries: ordinals and offsets are read, never
// searched.
func storedNumberField() *mapping.FieldMapping {
	fm := bleve.NewNumericFieldMapping()
	fm.IncludeInAll = false
	fm.Index = false
	fm.DocValues = false
	fm.Store = true
	return fm
}

// chunkTextField is both halves of the hybrid at chunk level: indexed so BM25
// can rank passages, stored so a kNN hit can quote one.
func chunkTextField() *mapping.FieldMapping {
	fm := bleve.NewTextFieldMapping()
	fm.Analyzer = AnalyzerName
	fm.IncludeInAll = false
	fm.Store = true
	// The stored copy is returned whole, so there is nothing to highlight and
	// term vectors would only double the posting storage of the largest field.
	fm.IncludeTermVectors = false
	return fm
}

func vectorField(dims int) *mapping.FieldMapping {
	fm := bleve.NewVectorBase64FieldMapping()
	fm.Dims = dims
	// Cosine, because every embedding provider we support returns vectors
	// meant to be compared by angle; bleve normalises both sides for us.
	fm.Similarity = index.CosineSimilarity
	// Recall over latency: an archive is small enough that the extra scan
	// costs milliseconds, and a passage that is never retrieved is the one
	// failure the whole feature exists to avoid.
	fm.VectorIndexOptimizedFor = index.IndexOptimizedForRecall
	return fm
}

// chunkText is the stored copy of a passage, capped.
func chunkText(text string) string {
	return strutil.TruncateRunes(strings.TrimSpace(text), maxChunkTextRunes)
}
