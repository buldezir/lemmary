// Package embed turns one document into stored chunk vectors.
//
// It sits between the chunker, the embedding client and the store so that the
// pipeline step and the backfill cron run exactly the same code: any difference
// between them would show up as an archive that is only half searchable.
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

// Retry backoff for a failed embedding. It starts where the worker's own step
// backoff ends and grows to six hours: the failures here are provider-shaped
// (a spent quota, a dead endpoint), and none of those repair in seconds.
const (
	retryBase = 5 * time.Minute
	retryMax  = 6 * time.Hour
)

type Result struct {
	// Nothing had to be sent: the stored chunks already describe this exact
	// text with this exact model.
	Skipped bool
	Chunks  int
	// The document was longer than the chunker's cap, so its tail is not
	// searchable. Recorded rather than logged, because the gap is invisible.
	Truncated    bool
	Dims         int
	PromptTokens int
	Requests     int
}

// Only the OCR text is compared, because only the OCR text is embedded: a
// rename or a retag carries the same vectors it did before.
//
// dims of 0 means the provider has not answered yet, so the stored length is
// unverified rather than wrong, and the document is left alone.
func IsFresh(state embedstore.State, model string, dims int, textHash string) bool {
	return state.Status == embedstore.StatusOK &&
		!state.Stale &&
		state.Model == model &&
		state.ChunkerVersion == chunk.Version &&
		state.TextHash == textHash &&
		(dims == 0 || state.Dims == dims)
}

// force re-embeds even when the stored chunks look current, which is what a
// reprocess run asks for. On a provider error the document is marked failed
// with a backoff and the previously stored chunks are left in place, because
// degraded retrieval beats no retrieval.
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

	// After the commit, never inside it: a listener reading the rows back must
	// not look before they are durable.
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

// Records that this exact text produced no passages at all. The row is the
// point: without it the backfill selects the document again on every tick
// forever. It reads as fresh until the model, dimensions or text change.
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
	// Replace dropped what a previous run stored; the derived index must too.
	embedstore.NotifyReplaced(app, doc.Id)
	return Result{Skipped: true, Dims: state.Dims}, nil
}

// The terminal row markNothingToEmbed writes. Separate because IsFresh has to
// accept it, or the loop it exists to break comes straight back.
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

// One pass so the orders cannot diverge: the vector at position i is the chunk
// at position i.
func plan(doc *core.Record, ocrText string, pieces []chunk.Chunk) ([]string, []embedstore.Chunk) {
	inputs := make([]string, 0, len(pieces))
	chunks := make([]embedstore.Chunk, 0, len(pieces))

	for _, piece := range pieces {
		text := ocrText[piece.Start:piece.End]
		if strings.TrimSpace(text) == "" {
			// The provider would refuse a whitespace-only chunk anyway.
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
