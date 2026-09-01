package embed

import (
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

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
		HeaderHash:     embedstore.TextHash("header"),
		Status:         embedstore.StatusOK,
	}
}

func TestIsFreshAcceptsAnUnchangedDocument(t *testing.T) {
	t.Parallel()
	state := freshState()

	if !IsFresh(state, state.Model, state.Dims, state.TextHash, state.HeaderHash) {
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
			if IsFresh(state, base.Model, base.Dims, base.TextHash, base.HeaderHash) {
				t.Fatalf("%s should not read as fresh", name)
			}
		})
	}

	if IsFresh(base, base.Model, base.Dims, embedstore.TextHash("edited body"), base.HeaderHash) {
		t.Fatal("changed OCR text should not read as fresh")
	}
	if IsFresh(base, base.Model, base.Dims, base.TextHash, embedstore.TextHash("edited header")) {
		t.Fatal("changed metadata should not read as fresh")
	}
}

// Before the provider has answered once there is no recorded length to compare
// against, and treating "unknown" as "wrong" would re-embed the whole archive
// on every tick.
func TestIsFreshIgnoresUnknownDimensions(t *testing.T) {
	t.Parallel()
	state := freshState()

	if !IsFresh(state, state.Model, 0, state.TextHash, state.HeaderHash) {
		t.Fatal("dims 0 means unverified, not wrong")
	}
}

func headerTestDocument(t *testing.T, set func(*core.Record)) *core.Record {
	t.Helper()
	collection := core.NewBaseCollection("documents")
	collection.Fields.Add(
		&core.TextField{Name: "title", Max: 500},
		&core.TextField{Name: "title_original", Max: 500},
		&core.TextField{Name: "purpose", Max: 2000},
		&core.TextField{Name: "summary", Max: 5000},
		&core.TextField{Name: "document_type", Max: 15},
		&core.TextField{Name: "correspondent", Max: 15},
		&core.TextField{Name: "document_date", Max: 30},
		&core.JSONField{Name: "people_or_organizations"},
		&core.TextField{Name: "ocr_text", Max: models.MaxOCRTextRunes},
	)
	record := core.NewRecord(collection)
	if set != nil {
		set(record)
	}
	return record
}

func TestHeaderForRendersTheDocumentsOwnFields(t *testing.T) {
	t.Parallel()
	doc := headerTestDocument(t, func(r *core.Record) {
		r.Set("title", "Stromrechnung Januar")
		r.Set("purpose", "Monthly electricity bill")
		r.Set("summary", "128,40 EUR due on 3 February.")
		r.Set("document_date", "2026-01-31")
		r.Set("people_or_organizations", []string{"Anna Muster"})
	})

	// A nil app is the "no relations resolvable" case; the plain fields must
	// still come through, because that is what most documents have.
	header := HeaderFor(nil, doc)
	if header.Title != "Stromrechnung Januar" || header.Summary != "128,40 EUR due on 3 February." {
		t.Fatalf("header = %+v", header)
	}
	if header.Date != "2026-01-31" || len(header.People) != 1 {
		t.Fatalf("header = %+v", header)
	}
	if header.DocumentType != "" || header.Correspondent != "" {
		t.Fatalf("unresolvable relations should be empty, got %+v", header)
	}

	text := header.Text()
	if !strings.Contains(text, "Title: Stromrechnung Januar") {
		t.Fatalf("header text = %q", text)
	}
}

func TestHeaderForEmptyDocumentRendersNothing(t *testing.T) {
	t.Parallel()
	doc := headerTestDocument(t, nil)

	if got := HeaderFor(nil, doc).Text(); got != "" {
		t.Fatalf("Text() = %q, want empty", got)
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

	inputs, chunks := plan(doc, "Title: Invoice", ocrText, pieces)

	if len(inputs) != len(chunks) {
		t.Fatalf("%d inputs for %d chunks", len(inputs), len(chunks))
	}
	if len(chunks) != len(pieces)+1 {
		t.Fatalf("got %d chunks for %d pieces plus a header", len(chunks), len(pieces))
	}
	if chunks[0].Kind != embedstore.KindHeader || chunks[0].Ordinal != 0 {
		t.Fatalf("chunk 0 = %+v, want the header at ordinal 0", chunks[0])
	}
	if inputs[0] != "Title: Invoice" {
		t.Fatalf("input 0 = %q", inputs[0])
	}
	for i, c := range chunks {
		if c.Ordinal != i {
			t.Fatalf("chunk %d has ordinal %d", i, c.Ordinal)
		}
		if c.DocumentID != "doc1" {
			t.Fatalf("chunk %d has document %q", i, c.DocumentID)
		}
	}
	for i := 1; i < len(chunks); i++ {
		if chunks[i].Kind != embedstore.KindBody {
			t.Fatalf("chunk %d = %+v, want a body chunk", i, chunks[i])
		}
		// Body chunks are offsets, not copies: the passage is sliced out of the
		// live column when it is read.
		if chunks[i].Text != "" {
			t.Fatalf("chunk %d stores text: %q", i, chunks[i].Text)
		}
		if inputs[i] != ocrText[chunks[i].StartByte:chunks[i].EndByte] {
			t.Fatalf("input %d does not match its stored range", i)
		}
	}
}

// A document with no metadata at all still has body chunks, and they must start
// at ordinal 0 rather than leaving a hole where the header would have been.
func TestPlanWithoutAHeaderStartsAtOrdinalZero(t *testing.T) {
	t.Parallel()
	ocrText := "A short note about the boiler service."
	doc := planTestDocument(t, ocrText)
	pieces, _ := chunk.Split(ocrText, chunk.DefaultOptions())

	inputs, chunks := plan(doc, "", ocrText, pieces)

	if len(chunks) != 1 || len(inputs) != 1 {
		t.Fatalf("got %d chunks / %d inputs, want 1 each", len(chunks), len(inputs))
	}
	if chunks[0].Kind != embedstore.KindBody || chunks[0].Ordinal != 0 {
		t.Fatalf("chunk = %+v", chunks[0])
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
