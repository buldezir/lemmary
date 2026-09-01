package embed

import (
	"strings"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/config"
	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/fulltext"
)

// ChunkSource reads stored chunks back out for the Bleve chunk index.
//
// It lives here rather than in embedstore because it is the mirror image of
// what this package writes: the same rule about which text a chunk stands for
// (a header chunk carries its own, a body chunk is a slice of ocr_text) has to
// hold on the way out, and keeping both halves in one package is what makes
// that checkable.
type ChunkSource struct{}

// NewChunkSource returns the source the index is built from.
func NewChunkSource() *ChunkSource { return &ChunkSource{} }

// Spec reports the embedding binding from the saved settings.
//
// Read from the database rather than from a runtime snapshot on purpose: this
// is called at boot, before the first reload has necessarily published one, and
// a wrong answer there would mean rebuilding a whole archive's vectors for
// nothing.
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

// SpecFrom projects a configuration onto the index's view of it. Exported so
// the settings-reload path can answer the same question from the snapshot it
// already holds, without a second read.
func SpecFrom(cfg config.Config) (fulltext.VectorSpec, bool) {
	if !config.HasEmbedding(cfg) {
		return fulltext.VectorSpec{}, false
	}
	spec := fulltext.VectorSpec{
		Model: strings.TrimSpace(cfg.EmbeddingModel),
		Dims:  cfg.EmbeddingDims,
	}
	// Dims is 0 until a provider has answered once. There is nothing to index
	// yet either, so reporting "off" is accurate rather than pessimistic: the
	// first embedding writes the number back, the settings reload lands, and
	// the index is built then.
	return spec, spec.Valid()
}

// ForDocument returns one document's chunks, resolved to text.
func (s *ChunkSource) ForDocument(app core.App, documentID string, spec fulltext.VectorSpec) ([]fulltext.Chunk, error) {
	if app == nil || strings.TrimSpace(documentID) == "" || !spec.Valid() {
		return nil, nil
	}
	rows, err := embedstore.Chunks(app.DB(), documentID)
	if err != nil {
		return nil, err
	}

	ocrText := ""
	loaded := false
	out := make([]fulltext.Chunk, 0, len(rows))
	for _, row := range rows {
		if !matchesSpec(row, spec) {
			continue
		}
		if row.Kind != embedstore.KindHeader && !loaded {
			ocrText = documentText(app, documentID)
			loaded = true
		}
		out = append(out, chunkFrom(row, ocrText))
	}
	return out, nil
}

// ForEach walks every stored chunk for spec.
//
// The scan is ordered by document, which is what makes the one-document text
// cache below enough: a document's ocr_text is read once however many chunks
// it was cut into.
func (s *ChunkSource) ForEach(app core.App, spec fulltext.VectorSpec, fn func(fulltext.Chunk) error) error {
	if app == nil || !spec.Valid() {
		return nil
	}
	currentID := ""
	currentText := ""
	loaded := false
	return embedstore.ForEachChunk(app.DB(), spec.Model, spec.Dims, func(row embedstore.Chunk) error {
		if row.DocumentID != currentID {
			currentID = row.DocumentID
			currentText = ""
			loaded = false
		}
		if row.Kind != embedstore.KindHeader && !loaded {
			currentText = documentText(app, row.DocumentID)
			loaded = true
		}
		return fn(chunkFrom(row, currentText))
	})
}

// Count is how many chunks the store holds for spec.
func (s *ChunkSource) Count(app core.App, spec fulltext.VectorSpec) (int, error) {
	if app == nil || !spec.Valid() {
		return 0, nil
	}
	return embedstore.CountChunks(app.DB(), spec.Model, spec.Dims)
}

func matchesSpec(row embedstore.Chunk, spec fulltext.VectorSpec) bool {
	return row.Model == spec.Model && row.Dims == spec.Dims && len(row.Vector) == spec.Dims
}

// chunkFrom converts a stored row, resolving a body chunk against the text it
// was cut from.
func chunkFrom(row embedstore.Chunk, ocrText string) fulltext.Chunk {
	c := fulltext.Chunk{
		DocumentID: row.DocumentID,
		UserID:     row.UserID,
		Ord:        row.Ordinal,
		StartByte:  row.StartByte,
		EndByte:    row.EndByte,
		Text:       row.Text,
		Vector:     row.Vector,
	}
	if row.Kind == embedstore.KindHeader {
		// The header chunk is rendered metadata; it has no place in the text
		// and must never be quoted as if it did.
		c.StartByte, c.EndByte = 0, 0
		return c
	}
	c.Text = sliceText(ocrText, row.StartByte, row.EndByte)
	return c
}

// sliceText is the chunk's slice of the document, or nothing when the offsets
// no longer fit — the text was re-OCRed since, and a clamped slice would be a
// quote from nowhere. The chunk is still indexed: its vector is unaffected.
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
