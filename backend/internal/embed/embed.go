// Package embed turns one document into stored chunk vectors.
//
// It sits between the chunker, the embedding client and the store so that the
// pipeline step and the backfill cron run exactly the same code: the two enter
// from opposite ends (a document that has just been processed, and a document
// the archive has been carrying since before the feature existed) and any
// difference between them would show up as an archive that is only half
// searchable.
package embed

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/embedstore"
)

// Retry backoff for a document whose embedding failed. It starts where the
// worker's own step backoff ends and grows to six hours, because the failures
// this sees are provider-shaped -- a spent quota, a dead endpoint, a model
// removed from a catalogue -- and none of those are repaired in seconds.
const (
	retryBase = 5 * time.Minute
	retryMax  = 6 * time.Hour
)

// Result reports what one document cost and what it produced.
type Result struct {
	// Skipped is true when nothing had to be sent: the stored chunks already
	// describe this exact text with this exact model.
	Skipped bool
	Chunks  int
	// Truncated is true when the document was longer than the chunker's cap, so
	// its tail is not searchable. Recorded rather than logged and forgotten,
	// because the gap is otherwise invisible.
	Truncated    bool
	Dims         int
	PromptTokens int
	Requests     int
}

// IsFresh reports whether stored chunks still describe the document as it is
// now, embedded with the model in use.
//
// Only the OCR text is compared, because only the OCR text is embedded: a
// document renamed, retagged or re-summarised carries the same vectors it did
// before, and asking a provider to confirm that costs money to learn nothing.
//
// dims of 0 means the provider has not answered yet, in which case the stored
// length cannot be wrong -- only unverified -- and the document is left alone.
func IsFresh(state embedstore.State, model string, dims int, textHash string) bool {
	return state.Status == embedstore.StatusOK &&
		!state.Stale &&
		state.Model == model &&
		state.ChunkerVersion == chunk.Version &&
		state.TextHash == textHash &&
		(dims == 0 || state.Dims == dims)
}

// EmbedDocument chunks, embeds and stores one document.
//
// force re-embeds even when the stored chunks look current, which is what a
// reprocess run asks for. On a provider error the document is marked failed
// with a backoff and the error is returned; the previously stored chunks are
// left in place, because degraded retrieval beats no retrieval.
func EmbedDocument(
	ctx context.Context,
	app core.App,
	embedder ai.Embedder,
	doc *core.Record,
	force bool,
	logger *slog.Logger,
) (Result, error) {
	if logger == nil {
		logger = app.Logger()
	}
	if embedder == nil {
		return Result{}, errors.New("embed: no embedding model is configured")
	}
	if doc == nil {
		return Result{}, errors.New("embed: no document")
	}

	ocrText := doc.GetString("ocr_text")
	textHash := embedstore.TextHash(ocrText)

	state, found, err := embedstore.Get(app.DB(), doc.Id)
	if err != nil {
		return Result{}, err
	}
	if !force && found && IsFresh(state, embedder.Model(), embedder.Dims(), textHash) {
		return Result{Skipped: true, Chunks: state.ChunkCount, Dims: state.Dims}, nil
	}

	if strings.TrimSpace(ocrText) == "" {
		return markNothingToEmbed(app, doc, embedder, textHash, logger)
	}

	pieces, truncated := chunk.Split(ocrText, chunk.DefaultOptions())
	inputs, chunks := plan(doc, ocrText, pieces)
	if len(inputs) == 0 {
		return markNothingToEmbed(app, doc, embedder, textHash, logger)
	}

	embedded, err := embedder.Embed(ctx, inputs)
	if err != nil {
		next := time.Now().Add(retryDelay(state.Attempts))
		if markErr := embedstore.MarkFailed(app.DB(), doc.Id, doc.GetString("user"), err, next); markErr != nil {
			logger.Warn("recording the embedding failure failed too",
				"document", doc.Id, slog.Any("error", markErr))
		}
		return Result{}, fmt.Errorf("embed document %s: %w", doc.Id, err)
	}
	if len(embedded.Vectors) != len(chunks) {
		return Result{}, fmt.Errorf("embed document %s: got %d vectors for %d chunks",
			doc.Id, len(embedded.Vectors), len(chunks))
	}

	dims := len(embedded.Vectors[0])
	for i := range chunks {
		chunks[i].Vector = embedded.Vectors[i]
	}

	next := embedstore.State{
		DocumentID:     doc.Id,
		UserID:         doc.GetString("user"),
		Model:          embedder.Model(),
		Dims:           dims,
		ChunkerVersion: chunk.Version,
		TextHash:       textHash,
		Truncated:      truncated,
		Status:         embedstore.StatusOK,
	}
	err = app.RunInTransaction(func(txApp core.App) error {
		return embedstore.Replace(txApp.DB(), next, chunks)
	})
	if err != nil {
		return Result{}, err
	}

	// After the commit, never inside it: a listener that reads the rows back
	// must not be able to look before they are durable.
	embedstore.NotifyReplaced(app, doc.Id)

	if truncated {
		logger.Warn("document was longer than the chunker's cap; its tail is not searchable",
			"document", doc.Id, "chunks", len(chunks))
	}
	return Result{
		Chunks:       len(chunks),
		Truncated:    truncated,
		Dims:         dims,
		PromptTokens: embedded.PromptTokens,
		Requests:     embedded.Requests,
	}, nil
}

// markNothingToEmbed records that this exact text produced no passages at all --
// a scan that OCRed to whitespace, or a document whose every chunk was blank.
//
// The row is the point. Without it the document has no state, so the backfill's
// candidate query selects it again on the next tick and every tick after that,
// paying for a record read and a chunker pass forever. Written with the current
// model, dimensions and text hash, it reads as fresh until one of them changes
// -- which is exactly when the question is worth asking again.
func markNothingToEmbed(
	app core.App,
	doc *core.Record,
	embedder ai.Embedder,
	textHash string,
	logger *slog.Logger,
) (Result, error) {
	state := emptyState(doc, embedder, textHash)
	err := app.RunInTransaction(func(txApp core.App) error {
		return embedstore.Replace(txApp.DB(), state, nil)
	})
	if err != nil {
		logger.Warn("recording an unembeddable document failed",
			"document", doc.Id, slog.Any("error", err))
		return Result{Skipped: true}, nil
	}
	// Replace dropped whatever a previous run stored, so the derived index has
	// to drop it too.
	embedstore.NotifyReplaced(app, doc.Id)
	return Result{Skipped: true, Dims: state.Dims}, nil
}

// emptyState is the terminal row markNothingToEmbed writes. Separate because it
// is the half worth testing: IsFresh has to accept it, or the loop it exists to
// break comes straight back.
func emptyState(doc *core.Record, embedder ai.Embedder, textHash string) embedstore.State {
	return embedstore.State{
		DocumentID:     doc.Id,
		UserID:         doc.GetString("user"),
		Model:          embedder.Model(),
		Dims:           embedder.Dims(),
		ChunkerVersion: chunk.Version,
		TextHash:       textHash,
		Status:         embedstore.StatusOK,
	}
}

// plan builds the inputs to embed and the rows to store, in one pass so their
// order cannot diverge: the vector at position i is the chunk at position i.
func plan(doc *core.Record, ocrText string, pieces []chunk.Chunk) ([]string, []embedstore.Chunk) {
	inputs := make([]string, 0, len(pieces))
	chunks := make([]embedstore.Chunk, 0, len(pieces))

	for _, piece := range pieces {
		text := ocrText[piece.Start:piece.End]
		if strings.TrimSpace(text) == "" {
			// A whitespace-only chunk would be refused by the provider and
			// embeds to nothing useful anyway.
			continue
		}
		chunks = append(chunks, embedstore.Chunk{
			DocumentID: doc.Id,
			Ordinal:    len(chunks),
			StartByte:  piece.Start,
			EndByte:    piece.End,
		})
		inputs = append(inputs, text)
	}
	return inputs, chunks
}

// retryDelay doubles per recorded failure and stops at retryMax.
func retryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	delay := retryBase
	for i := 0; i < attempts; i++ {
		delay *= 2
		if delay >= retryMax {
			return retryMax
		}
	}
	return delay
}
