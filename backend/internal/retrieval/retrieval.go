// Package retrieval holds the ranking arithmetic Deep Search runs on top of the
// index: fusing ranked lists, picking a document's passages, and excerpting.
//
// Pure Go over plain values: no PocketBase, no Bleve, no provider client, which
// is what makes retrieval quality testable without a running app.
package retrieval

import "context"

// ChunkHit is one passage-sized piece of a document. The byte offsets index the
// document's OCR text; Text may be empty when the caller is expected to slice
// those offsets itself. The chunk index converts its own hits to this, rather
// than this package importing an index.
type ChunkHit struct {
	DocumentID string
	Ord        int
	Page       int
	Score      float64
	StartByte  int
	EndByte    int
	Text       string
}

type ChunkQuery struct {
	Vector []float32
	Text   string
	UserID string
	// SharedDocumentIDs pass the UserID filter too: a chunk carries its owner,
	// and these documents were shared with that user by someone else.
	SharedDocumentIDs []string
	DocumentIDs       []string
	K                 int
}

// Nil is a valid ChunkSearcher everywhere one is held: the dense path is
// then skipped.
type ChunkSearcher interface {
	SearchChunks(ctx context.Context, q ChunkQuery) ([]ChunkHit, error)
}

// The production embedder also reports token usage and is adapted at the single
// call site that needs it.
type Embedder interface {
	Embed(ctx context.Context, inputs []string) ([][]float32, error)
}
