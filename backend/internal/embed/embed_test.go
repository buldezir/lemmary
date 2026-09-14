package embed

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ai"
	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/models"
)

func freshState() embedstore.State {
	return embedstore.State{
		DocumentID:     "doc1",
		Model:          "text-embedding-3-small",
		Dims:           1536,
		ChunkerVersion: chunk.Version,
		TextHash:       embedstore.TextHash("body"),
		Status:         embedstore.StatusOK,
	}
}

func TestIsFreshAcceptsAnUnchangedDocument(t *testing.T) {
	t.Parallel()
	state := freshState()

	if !IsFresh(state, state.Model, state.Dims, state.TextHash) {
		t.Fatal("an unchanged document should be fresh")
	}
}

func TestIsFreshRejectsEveryReasonToReEmbed(t *testing.T) {
	t.Parallel()
	base := freshState()

	cases := map[string]func(*embedstore.State){
		"a failed run":         func(s *embedstore.State) { s.Status = embedstore.StatusFailed },
		"an edit marked stale": func(s *embedstore.State) { s.Stale = true },
		"a different model":    func(s *embedstore.State) { s.Model = "other-model" },
		"an older chunker":     func(s *embedstore.State) { s.ChunkerVersion = chunk.Version - 1 },
		"different dimensions": func(s *embedstore.State) { s.Dims = 3072 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			state := base
			mutate(&state)
			if IsFresh(state, base.Model, base.Dims, base.TextHash) {
				t.Fatalf("%s should not read as fresh", name)
			}
		})
	}

	if IsFresh(base, base.Model, base.Dims, embedstore.TextHash("edited body")) {
		t.Fatal("changed OCR text should not read as fresh")
	}
}

// Before the provider has answered once there is no recorded length to compare
// against, and treating "unknown" as "wrong" would re-embed the whole archive
// on every tick.
func TestIsFreshIgnoresUnknownDimensions(t *testing.T) {
	t.Parallel()
	state := freshState()

	if !IsFresh(state, state.Model, 0, state.TextHash) {
		t.Fatal("dims 0 means unverified, not wrong")
	}
}

func planTestDocument(t *testing.T, ocrText string) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection("documents")
	collection.Fields.Add(&core.TextField{Name: "ocr_text", Max: models.MaxOCRTextRunes})
	record := core.NewRecord(collection)
	record.Id = "doc1"
	record.Set("ocr_text", ocrText)
	return record
}

// The vector at position i is stored on the chunk at position i, so the two
// slices have to be built together and stay the same length.
func TestPlanPairsInputsWithChunks(t *testing.T) {
	t.Parallel()
	ocrText := strings.Repeat("Die Rechnung wurde bezahlt. ", 300)
	doc := planTestDocument(t, ocrText)
	pieces, _ := chunk.Split(ocrText, chunk.DefaultOptions())

	inputs, chunks := plan(doc, ocrText, pieces)

	if len(inputs) != len(chunks) {
		t.Fatalf("%d inputs for %d chunks", len(inputs), len(chunks))
	}
	if len(chunks) != len(pieces) {
		t.Fatalf("got %d chunks for %d pieces", len(chunks), len(pieces))
	}
	for i, c := range chunks {
		// Ordinals start at 0 and have no holes: they are what the Bleve index
		// and the passage layer address a passage by.
		if c.Ordinal != i {
			t.Fatalf("chunk %d has ordinal %d", i, c.Ordinal)
		}
		if c.DocumentID != "doc1" {
			t.Fatalf("chunk %d has document %q", i, c.DocumentID)
		}
		// A chunk is offsets, not a copy: the passage is sliced out of the live
		// column when it is read, so the offsets have to address the very text
		// that was sent to the provider.
		if inputs[i] != ocrText[c.StartByte:c.EndByte] {
			t.Fatalf("input %d does not match its stored range", i)
		}
	}
}

// Nothing but the OCR text is embedded, so a document rich in metadata and
// short on text produces exactly the chunks its text was cut into -- no
// metadata passage, and no hole at ordinal 0 where one used to sit.
func TestPlanEmbedsOnlyTheOCRText(t *testing.T) {
	t.Parallel()
	ocrText := "A short note about the boiler service."
	doc := planTestDocument(t, ocrText)
	doc.Set("title", "Boiler service invoice")
	pieces, _ := chunk.Split(ocrText, chunk.DefaultOptions())

	inputs, chunks := plan(doc, ocrText, pieces)

	if len(chunks) != 1 || len(inputs) != 1 {
		t.Fatalf("got %d chunks / %d inputs, want 1 each", len(chunks), len(inputs))
	}
	if chunks[0].Ordinal != 0 {
		t.Fatalf("chunk = %+v, want ordinal 0", chunks[0])
	}
	if inputs[0] != ocrText {
		t.Fatalf("input 0 = %q, want the OCR text alone", inputs[0])
	}
}

// stubEmbedder is a binding, not a client: the tests that use it never embed
// anything, they only ask what model and length a row should record.
type stubEmbedder struct {
	model string
	dims  int
}

func (s stubEmbedder) Name() string  { return "stub" }
func (s stubEmbedder) Model() string { return s.model }
func (s stubEmbedder) Dims() int     { return s.dims }
func (s stubEmbedder) Embed(context.Context, []string) (ai.EmbedResult, error) {
	return ai.EmbedResult{}, errors.New("stubEmbedder does not embed")
}

// A document that yields no passages -- a scan that OCRed to whitespace, or one
// whose every chunk was blank -- used to return Skipped without writing
// anything, so the backfill selected it again on the next tick and on every tick
// after that. The row it writes now has to read as fresh, or the loop simply
// comes back.
func TestEmptyStateStopsTheDocumentComingBack(t *testing.T) {
	t.Parallel()
	embedder := stubEmbedder{model: "text-embedding-3-small", dims: 1536}
	doc := planTestDocument(t, "\n\n  \t\n")
	textHash := embedstore.TextHash(doc.GetString("ocr_text"))

	state := emptyState(doc, embedder, textHash)

	if state.DocumentID != doc.Id || state.Status != embedstore.StatusOK || state.ChunkCount != 0 {
		t.Fatalf("terminal row = %+v", state)
	}
	if !IsFresh(state, embedder.Model(), embedder.Dims(), textHash) {
		t.Fatalf("the terminal row does not read as fresh: %+v", state)
	}
	// It is terminal for this text only: re-OCR the document and it is a
	// candidate again, which is the one moment asking again is worth anything.
	if IsFresh(state, embedder.Model(), embedder.Dims(), embedstore.TextHash("real text now")) {
		t.Fatal("re-OCRed text should not be covered by the previous terminal row")
	}
	if IsFresh(state, "another-model", embedder.Dims(), textHash) {
		t.Fatal("a model switch should not be covered by the previous terminal row")
	}
}

func TestRetryDelayGrowsAndStops(t *testing.T) {
	t.Parallel()

	if got := retryDelay(0); got != retryBase {
		t.Fatalf("retryDelay(0) = %v, want %v", got, retryBase)
	}
	if got := retryDelay(1); got != 2*retryBase {
		t.Fatalf("retryDelay(1) = %v, want %v", got, 2*retryBase)
	}
	if got := retryDelay(3); got != 8*retryBase {
		t.Fatalf("retryDelay(3) = %v, want %v", got, 8*retryBase)
	}
	if got := retryDelay(50); got != retryMax {
		t.Fatalf("retryDelay(50) = %v, want the %v cap", got, retryMax)
	}
	if got := retryDelay(-1); got != retryBase {
		t.Fatalf("retryDelay(-1) = %v, want %v", got, retryBase)
	}
	// The cap has to be reachable in a working day, or a document that failed
	// once during an outage would effectively never come back.
	if retryMax > 24*time.Hour {
		t.Fatalf("retryMax = %v", retryMax)
	}
}
