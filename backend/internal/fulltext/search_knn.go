package fulltext

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/retrieval"
)

// ErrVectorDims is returned when a query vector is not the length the index was
// built for. It is a configuration error, not a ranking one: bleve drops a
// wrong-length vector without a word, so a caller that ignored this would get
// an empty result list and no reason for it.
var ErrVectorDims = errors.New("query vector does not match the indexed dimensions")

// errChunksNotReady is what every chunk operation returns when no chunk index
// is open. Callers degrade to keyword search on it rather than failing.
var errChunksNotReady = errors.New("chunk index is not ready")

// defaultChunkK is the number of passages a chunk search returns when the
// caller does not say.
const defaultChunkK = 20

// SearchChunks answers a chunk-level query by vector, by text, or by both.
//
// With both, the fusion is Bleve's own: the lexical and the kNN list are
// produced by one index over one set of ids, which is the one case where the
// engine can fuse them itself and the Go-side RRF in the retrieval package
// would only be re-doing its work with less information.
//
// Filtering is by user only. Every other filter the agent may have applied is a
// property of the document, not of the passage, and denormalising those into
// three million chunk rows would mean a tag rename rewriting vectors; the
// caller resolves them against the documents index and passes DocumentIDs.
func (i *Index) SearchChunks(ctx context.Context, q retrieval.ChunkQuery) ([]retrieval.ChunkHit, error) {
	if !i.ChunksReady() {
		return nil, nil
	}
	text := strings.TrimSpace(q.Text)
	if len(q.Vector) == 0 && text == "" {
		return nil, nil
	}
	if dims := i.VectorSpec().Dims; len(q.Vector) > 0 && len(q.Vector) != dims {
		return nil, fmt.Errorf("%w: query has %d, index has %d", ErrVectorDims, len(q.Vector), dims)
	}
	if ctx == nil {
		ctx = context.Background()
	}

	k := q.K
	if k <= 0 {
		k = defaultChunkK
	}
	if int64(k) > bleve.BleveMaxK {
		k = int(bleve.BleveMaxK)
	}

	filter := chunkFilter(q)
	var hits []retrieval.ChunkHit
	err := i.withChunkIndex(func(b bleve.Index) error {
		var req *bleve.SearchRequest
		switch {
		case text != "":
			bq := chunkTextQuery(text)
			if filter != nil {
				bq = bleve.NewConjunctionQuery(bq, filter)
			}
			req = bleve.NewSearchRequestOptions(bq, k, 0, false)
		default:
			// kNN alone still needs a query; match-none is the idiom, the
			// filter rides on the kNN clause itself.
			req = bleve.NewSearchRequestOptions(bleve.NewMatchNoneQuery(), k, 0, false)
		}
		req.Fields = []string{
			FieldChunkDocumentID, FieldChunkOrd, FieldChunkPage,
			FieldChunkStart, FieldChunkEnd, FieldChunkText,
		}
		if len(q.Vector) > 0 {
			req.AddKNNWithFilter(FieldChunkVector, q.Vector, int64(k), 1.0, filter)
			if text != "" {
				req.Score = bleve.ScoreRRF
				req.Params = bleve.NewDefaultParams(0, k)
			}
		}

		res, err := b.SearchInContext(ctx, req)
		if err != nil {
			return fmt.Errorf("bleve chunk search: %w", err)
		}
		hits = make([]retrieval.ChunkHit, 0, len(res.Hits))
		for _, h := range res.Hits {
			hits = append(hits, chunkHitFrom(h.ID, h.Score, h.Fields))
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, errChunksNotReady) {
			return nil, nil
		}
		return nil, err
	}
	return hits, nil
}

// chunkTextQuery is the lexical half at passage level. Relaxed in the same
// sense the document query is (see fulltext.Query.Relaxed): the text is a
// question the model turned into keywords, and a passage that carries most of
// them is the answer far more often than one that carries all of them.
func chunkTextQuery(text string) query.Query {
	parts := parseQueryParts(text)
	if len(parts) == 0 {
		mq := bleve.NewMatchQuery(text)
		mq.SetField(FieldChunkText)
		mq.Analyzer = AnalyzerName
		return mq
	}

	must := make([]query.Query, 0, len(parts))
	should := make([]query.Query, 0, len(parts))
	for _, part := range parts {
		if part.phrase {
			pq := bleve.NewMatchPhraseQuery(part.text)
			pq.SetField(FieldChunkText)
			pq.Analyzer = AnalyzerName
			must = append(must, pq)
			continue
		}
		mq := bleve.NewMatchQuery(part.text)
		mq.SetField(FieldChunkText)
		mq.Analyzer = AnalyzerName
		should = append(should, mq)

		if fuzzyWorthy(part.text) {
			fq := bleve.NewMatchQuery(part.text)
			fq.SetField(FieldChunkText)
			fq.Analyzer = AnalyzerName
			fq.SetFuzziness(1)
			fq.SetPrefix(1)
			fq.SetBoost(0.5)
			should = append(should, fq)
		}
	}
	if len(should) > 0 {
		dq := bleve.NewDisjunctionQuery(should...)
		// One passage is a fraction of a document, so a chunk carrying most of
		// the query's words is already a strong signal; the document-level
		// minimum would rule out the short passage that answers the question
		// in five words.
		dq.SetMin(1)
		must = append(must, dq)
	}
	if len(must) == 1 {
		return must[0]
	}
	return bleve.NewConjunctionQuery(must...)
}

// chunkFilter is the pre-filter both halves of the query run under: the caller's
// own passages, and — when the agent's search had filters the chunk index does
// not carry — the documents those filters resolved to.
func chunkFilter(q retrieval.ChunkQuery) query.Query {
	conjuncts := make([]query.Query, 0, 2)
	if user := strings.TrimSpace(q.UserID); user != "" {
		conjuncts = append(conjuncts, termQuery(FieldChunkUser, user))
	}
	if ids := anyTermQuery(FieldChunkDocumentID, q.DocumentIDs); ids != nil {
		conjuncts = append(conjuncts, ids)
	}
	switch len(conjuncts) {
	case 0:
		return nil
	case 1:
		return conjuncts[0]
	default:
		return bleve.NewConjunctionQuery(conjuncts...)
	}
}

// chunkHitFrom rebuilds a hit from the stored fields. A kNN hit has no
// fragments and no source document, so everything here comes from storage; the
// id is the fallback for the two fields that identify the passage.
func chunkHitFrom(id string, score float64, fields map[string]any) retrieval.ChunkHit {
	hit := retrieval.ChunkHit{Score: score}
	if s, ok := fields[FieldChunkDocumentID].(string); ok {
		hit.DocumentID = s
	}
	if s, ok := fields[FieldChunkText].(string); ok {
		hit.Text = s
	}
	hit.Ord = intField(fields, FieldChunkOrd)
	hit.Page = intField(fields, FieldChunkPage)
	hit.StartByte = intField(fields, FieldChunkStart)
	hit.EndByte = intField(fields, FieldChunkEnd)
	if hit.DocumentID == "" {
		if docID, ord, ok := splitChunkDocID(id); ok {
			hit.DocumentID = docID
			hit.Ord = ord
		}
	}
	return hit
}

func intField(fields map[string]any, name string) int {
	if v, ok := fields[name].(float64); ok {
		return int(v)
	}
	return 0
}

// idsByKeyword pages every id whose keyword field equals value.
func idsByKeyword(b bleve.Index, field, value string) ([]string, error) {
	page := lookupPageSize
	if page <= 0 {
		page = defaultLookupPage
	}
	tq := bleve.NewTermQuery(value)
	tq.SetField(field)

	var ids []string
	offset := 0
	for {
		req := bleve.NewSearchRequestOptions(tq, page, offset, false)
		res, err := b.Search(req)
		if err != nil {
			return ids, err
		}
		for _, h := range res.Hits {
			ids = append(ids, h.ID)
		}
		if len(res.Hits) < page {
			return ids, nil
		}
		offset += page
	}
}

// logChunkTaskError reports an async chunk failure. The app handle is absent on
// the delete path (a delete needs no database), so the error is dropped rather
// than logged there: it is one document's passages in an index that the boot
// heal rebuilds anyway.
func logChunkTaskError(app core.App, message, documentID string, err error) {
	if app == nil {
		return
	}
	app.Logger().Error(message, slog.String("id", documentID), slog.Any("error", err))
}
