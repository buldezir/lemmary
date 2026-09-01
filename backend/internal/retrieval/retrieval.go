// Package retrieval holds the ranking arithmetic Deep Search runs on top of the
// index: fusing several ranked lists into one, picking the passages a document
// is represented by, and excerpting a long document around a focus.
//
// Everything here is pure Go over plain values. No PocketBase, no Bleve, no
// provider client: the same functions rank a real Bleve result list, a fake
// in-memory one, and the eval corpus, which is what makes retrieval quality
// testable without a running app.
package retrieval

import "context"

// ChunkHit is one passage-sized piece of a document, as any chunk-level search
// returns it. The byte offsets index the document's OCR text; Text may be empty
// when the caller is expected to slice those offsets itself.
//
// This is the vocabulary type: the chunk index converts its own hits to this,
// rather than this package importing an index.
type ChunkHit struct {
	DocumentID string
	Ord        int
	Page       int
	Score      float64
	StartByte  int
	EndByte    int
	Text       string
}

// ChunkQuery asks for the K chunks closest to a vector, to a text, or to both,
// restricted to one user and optionally to a set of documents.
type ChunkQuery struct {
	Vector      []float32
	Text        string
	UserID      string
	DocumentIDs []string
	K           int
}

// ChunkSearcher is the chunk-level index as the retriever sees it. Nil is a
// valid value everywhere one is held: the dense path is then skipped.
type ChunkSearcher interface {
	SearchChunks(ctx context.Context, q ChunkQuery) ([]ChunkHit, error)
}

// Embedder turns texts into vectors. Only this package's fakes and the tests
// implement it; the production embedder also reports token usage and is adapted
// at the single call site that needs it.
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}
