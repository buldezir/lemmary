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
	"lemmary/backend/internal/models"
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
// dims of 0 means the provider has not answered yet, in which case the stored
// length cannot be wrong -- only unverified -- and the document is left alone.
func IsFresh(state embedstore.State, model string, dims int, textHash, headerHash string) bool {
	return state.Status == embedstore.StatusOK &&
		!state.Stale &&
		state.Model == model &&
		state.ChunkerVersion == chunk.Version &&
		state.TextHash == textHash &&
		state.HeaderHash == headerHash &&
		(dims == 0 || state.Dims == dims)
}

// HeaderFor renders a document's metadata as the passage embedded at ordinal 0.
//
// The relations are resolved to names here rather than stored as ids: "Invoice"
// is what a question is asked in, and an id embeds to noise.
func HeaderFor(app core.App, doc *core.Record) chunk.Header {
	tagIDs := doc.GetStringSlice("tags")
	tags := make([]string, 0, len(tagIDs))
	for _, id := range tagIDs {
		if name := lookupName(app, "tags", id); name != "" {
			tags = append(tags, name)
		}
	}
	return chunk.Header{
		Title:           strings.TrimSpace(doc.GetString("title")),
		TitleOriginal:   strings.TrimSpace(doc.GetString("title_original")),
		Purpose:         strings.TrimSpace(doc.GetString("purpose")),
		PurposeOriginal: strings.TrimSpace(doc.GetString("purpose_original")),
		Summary:         strings.TrimSpace(doc.GetString("summary")),
		SummaryOriginal: strings.TrimSpace(doc.GetString("summary_original")),
		DocumentType:    lookupName(app, "document_types", doc.GetString("document_type")),
		Correspondent:   lookupName(app, "correspondents", doc.GetString("correspondent")),
		Date:            strings.TrimSpace(doc.GetString("document_date")),
		Tags:            tags,
		People:          models.PeopleOrOrganizations(doc),
	}
}

func lookupName(app core.App, collection, id string) string {
	id = strings.TrimSpace(id)
	if app == nil || id == "" {
		return ""
	}
	record, err := app.FindRecordById(collection, id)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(record.GetString("name"))
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
	headerText := HeaderFor(app, doc).Text()
	textHash := embedstore.TextHash(ocrText)
	headerHash := embedstore.TextHash(headerText)

	state, found, err := embedstore.Get(app.DB(), doc.Id)
	if err != nil {
		return Result{}, err
	}
	if !force && found && IsFresh(state, embedder.Model(), embedder.Dims(), textHash, headerHash) {
		return Result{Skipped: true, Chunks: state.ChunkCount, Dims: state.Dims}, nil
	}

	if strings.TrimSpace(ocrText) == "" {
		return markNothingToEmbed(app, doc, embedder, textHash, headerHash, logger)
	}

	pieces, truncated := chunk.Split(ocrText, chunk.DefaultOptions())
	inputs, chunks := plan(doc, headerText, ocrText, pieces)
	if len(inputs) == 0 {
		return markNothingToEmbed(app, doc, embedder, textHash, headerHash, logger)
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
		HeaderHash:     headerHash,
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

// markNothingToEmbed records that this exact text and metadata produced no
// passages at all -- a scan that OCRed to whitespace, or a document whose every
// chunk was blank.
//
// The row is the point. Without it the document has no state, so the backfill's
// candidate query selects it again on the next tick and every tick after that,
// paying for a record read and a chunker pass forever. Written with the current
// model, dimensions and hashes, it reads as fresh until one of them changes --
// which is exactly when the question is worth asking again.
func markNothingToEmbed(
	app core.App,
	doc *core.Record,
	embedder ai.Embedder,
	textHash, headerHash string,
	logger *slog.Logger,
) (Result, error) {
	state := emptyState(doc, embedder, textHash, headerHash)
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
func emptyState(doc *core.Record, embedder ai.Embedder, textHash, headerHash string) embedstore.State {
	return embedstore.State{
		DocumentID:     doc.Id,
		UserID:         doc.GetString("user"),
		Model:          embedder.Model(),
		Dims:           embedder.Dims(),
		ChunkerVersion: chunk.Version,
		TextHash:       textHash,
		HeaderHash:     headerHash,
		Status:         embedstore.StatusOK,
	}
}

// plan builds the inputs to embed and the rows to store, in one pass so their
// order cannot diverge: the vector at position i is the chunk at position i.
func plan(doc *core.Record, headerText, ocrText string, pieces []chunk.Chunk) ([]string, []embedstore.Chunk) {
	inputs := make([]string, 0, len(pieces)+1)
	chunks := make([]embedstore.Chunk, 0, len(pieces)+1)

	if strings.TrimSpace(headerText) != "" {
		inputs = append(inputs, headerText)
		chunks = append(chunks, embedstore.Chunk{
			DocumentID: doc.Id,
			Ordinal:    0,
			Kind:       embedstore.KindHeader,
			// The header is not a slice of ocr_text, so it carries its own copy;
			// it is a few hundred bytes, not a second archive.
			Text: headerText,
		})
	}
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
			Kind:       embedstore.KindBody,
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
