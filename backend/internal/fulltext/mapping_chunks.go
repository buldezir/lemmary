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

	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/strutil"
)

// Only the first field of the version file: the model and its dimensions are
// part of the same string, because a vector field's length is fixed at mapping
// time and vectors of another length are dropped silently.
const ChunkMappingVersion = "2"

// Deliberately few: everything beyond "which passage, of whose document" is
// read from SQLite by document id, so a rename never has to touch a chunk.
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

// Derived from the chunker's own ceiling rather than guessed at: the stored
// copy is what retrieval quotes, so a lower cap would silently truncate the
// tail of every full-size chunk.
var maxChunkTextRunes = chunk.DefaultOptions().MaxRunes

// VectorSpec is the embedding binding the chunk index is built for. Both halves
// matter: two models with the same dimension count produce vectors that mean
// nothing to each other, so a change to either wipes the index.
type VectorSpec struct {
	Model string
	Dims  int
}

// Dims is 0 until the first embedding response comes back, the normal state of
// a fresh install with a model configured and nothing embedded yet.
func (s VectorSpec) Valid() bool {
	return strings.TrimSpace(s.Model) != "" &&
		s.Dims >= mapping.MinVectorDims && s.Dims <= mapping.MaxVectorDims
}

func (s VectorSpec) normalized() VectorSpec {
	return VectorSpec{Model: strings.TrimSpace(s.Model), Dims: s.Dims}
}

func (s VectorSpec) version() string {
	return fmt.Sprintf("%s;model=%s;dims=%d", ChunkMappingVersion, s.Model, s.Dims)
}

// Uses the documents index analyzer (see newMapping): two analyzers over the
// same words would make the lexical half of a hybrid search disagree with itself.
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

	// A kNN hit carries no highlight fragments, so a passage must come back
	// from storage.
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

// No numeric range index: ordinals and offsets are read, never searched.
func storedNumberField() *mapping.FieldMapping {
	fm := bleve.NewNumericFieldMapping()
	fm.IncludeInAll = false
	fm.Index = false
	fm.DocValues = false
	fm.Store = true
	return fm
}

// Indexed so BM25 can rank passages, stored so a kNN hit can quote one.
func chunkTextField() *mapping.FieldMapping {
	fm := bleve.NewTextFieldMapping()
	fm.Analyzer = AnalyzerName
	fm.IncludeInAll = false
	fm.Store = true
	// Returned whole, so nothing to highlight and term vectors would only
	// double the posting storage of the largest field.
	fm.IncludeTermVectors = false
	return fm
}

func vectorField(dims int) *mapping.FieldMapping {
	fm := bleve.NewVectorBase64FieldMapping()
	fm.Dims = dims
	// Every provider we support returns vectors meant to be compared by angle.
	fm.Similarity = index.CosineSimilarity
	// Recall over latency: an archive is small enough that the extra scan
	// costs milliseconds.
	fm.VectorIndexOptimizedFor = index.IndexOptimizedForRecall
	return fm
}

func chunkText(text string) string {
	return strutil.TruncateRunes(strings.TrimSpace(text), maxChunkTextRunes)
}
