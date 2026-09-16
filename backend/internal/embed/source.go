package embed

import (
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/fulltext"
)

// ChunkSource reads stored chunks back out for the Bleve chunk index. It lives
// here rather than in embedstore because the rule that a chunk is a slice of
// ocr_text has to hold on the way out too.
type ChunkSource struct{}

func NewChunkSource() *ChunkSource { return &ChunkSource{} }

// Read from the database rather than a runtime snapshot: this runs at boot,
// before the first reload has necessarily published one, and a wrong answer
// would rebuild a whole archive's vectors for nothing.
func (s *ChunkSource) Spec(app core.App) (fulltext.VectorSpec, bool) {
	if app == nil {
		return fulltext.VectorSpec{}, false
	}
	cfg, err := config.Load(app)
	if err != nil {
		app.Logger().Warn("chunk index cannot read the embedding settings", "error", err)
		return fulltext.VectorSpec{}, false
	}
	return SpecFrom(cfg)
}

// Exported so the settings-reload path can answer the same question from the
// snapshot it already holds, without a second read.
func SpecFrom(cfg config.Config) (fulltext.VectorSpec, bool) {
	if !config.HasEmbedding(cfg) {
		return fulltext.VectorSpec{}, false
	}
	spec := fulltext.VectorSpec{
		Model: strings.TrimSpace(cfg.EmbeddingModel),
		Dims:  cfg.EmbeddingDims,
	}
	// Dims is 0 until a provider has answered once, and there is nothing to
	// index yet either, so reporting "off" is accurate rather than pessimistic.
	return spec, spec.Valid()
}

func (s *ChunkSource) ForDocument(app core.App, documentID string, spec fulltext.VectorSpec) ([]fulltext.Chunk, error) {
	if app == nil || strings.TrimSpace(documentID) == "" || !spec.Valid() {
		return nil, nil
	}
	rows, err := embedstore.Chunks(app.DB(), documentID)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		// Reading the record here would be a fetch per unembedded document on
		// every index pass.
		return nil, nil
	}

	ocrText := documentText(app, documentID)
	out := make([]fulltext.Chunk, 0, len(rows))
	for _, row := range rows {
		if !matchesSpec(row, spec) {
			continue
		}
		out = append(out, chunkFrom(row, ocrText))
	}
	return out, nil
}

// The scan is ordered by document, which is what makes the one-document text
// cache below enough.
func (s *ChunkSource) ForEach(app core.App, spec fulltext.VectorSpec, fn func(fulltext.Chunk) error) error {
	if app == nil || !spec.Valid() {
		return nil
	}
	currentID := ""
	currentText := ""
	return embedstore.ForEachChunk(app.DB(), spec.Model, spec.Dims, func(row embedstore.Chunk) error {
		if row.DocumentID != currentID {
			currentID = row.DocumentID
			currentText = documentText(app, row.DocumentID)
		}
		return fn(chunkFrom(row, currentText))
	})
}

func (s *ChunkSource) Count(app core.App, spec fulltext.VectorSpec) (int, error) {
	if app == nil || !spec.Valid() {
		return 0, nil
	}
	return embedstore.CountChunks(app.DB(), spec.Model, spec.Dims)
}

func matchesSpec(row embedstore.Chunk, spec fulltext.VectorSpec) bool {
	return row.Model == spec.Model && row.Dims == spec.Dims && len(row.Vector) == spec.Dims
}

func chunkFrom(row embedstore.Chunk, ocrText string) fulltext.Chunk {
	return fulltext.Chunk{
		DocumentID: row.DocumentID,
		UserID:     row.UserID,
		Ord:        row.Ordinal,
		StartByte:  row.StartByte,
		EndByte:    row.EndByte,
		Text:       sliceText(ocrText, row.StartByte, row.EndByte),
		Vector:     row.Vector,
	}
}

// Nothing when the offsets no longer fit: the text was re-OCRed since, and a
// clamped slice would be a quote from nowhere. The chunk is still indexed.
func sliceText(ocrText string, start, end int) string {
	if start < 0 || end <= start || end > len(ocrText) {
		return ""
	}
	for start > 0 && start < len(ocrText) && !utf8.RuneStart(ocrText[start]) {
		start--
	}
	for end < len(ocrText) && !utf8.RuneStart(ocrText[end]) {
		end++
	}
	return strings.TrimSpace(ocrText[start:end])
}

func documentText(app core.App, documentID string) string {
	record, err := app.FindRecordById("documents", documentID)
	if err != nil {
		return ""
	}
	return record.GetString("ocr_text")
}

var _ fulltext.ChunkSource = (*ChunkSource)(nil)
