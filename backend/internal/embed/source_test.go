package embed

import (
	"strings"
	"testing"

	"lemmary/backend/internal/aiprovider"
	"lemmary/backend/internal/config"
	"lemmary/backend/internal/embedstore"
	"lemmary/backend/internal/fulltext"
)

func TestSpecFromNeedsAModelAndADimensionCount(t *testing.T) {
	provider := &aiprovider.Provider{SDK: aiprovider.SDKOpenAI, APIKey: "sk-test"}

	spec, ok := SpecFrom(config.Config{
		EmbeddingProvider: provider,
		EmbeddingModel:    " text-embedding-3-small ",
		EmbeddingDims:     1536,
	})
	if !ok || spec.Model != "text-embedding-3-small" || spec.Dims != 1536 {
		t.Fatalf("spec = %+v, ok = %v", spec, ok)
	}

	// Before the first provider response the dimension count is unknown, and a
	// vector field cannot be built without one.
	if _, ok := SpecFrom(config.Config{
		EmbeddingProvider: provider,
		EmbeddingModel:    "text-embedding-3-small",
	}); ok {
		t.Fatal("a spec with no dimensions must not open an index")
	}

	// The binding cleared: the index is removed rather than left stale.
	if _, ok := SpecFrom(config.Config{EmbeddingDims: 1536}); ok {
		t.Fatal("no embedding binding must report off")
	}
}

func TestChunkFromResolvesBodyTextAndLeavesTheHeaderAlone(t *testing.T) {
	ocr := "Die monatliche Kaltmiete beträgt 1234 EUR. Die Kaution beträgt 3702 EUR."
	start := strings.Index(ocr, "Die Kaution")

	body := chunkFrom(embedstore.Chunk{
		DocumentID: "doc1",
		UserID:     "u1",
		Ordinal:    2,
		Kind:       embedstore.KindBody,
		StartByte:  start,
		EndByte:    len(ocr),
		Vector:     []float32{1, 0},
	}, ocr)
	if body.Text != "Die Kaution beträgt 3702 EUR." {
		t.Fatalf("body text = %q", body.Text)
	}
	if body.StartByte != start || body.EndByte != len(ocr) {
		t.Fatalf("offsets were not carried: %+v", body)
	}

	// The header chunk is rendered metadata: it stores its own text and points
	// at nothing in the document, so it must never be quoted with offsets.
	header := chunkFrom(embedstore.Chunk{
		DocumentID: "doc1",
		Kind:       embedstore.KindHeader,
		StartByte:  0,
		EndByte:    40,
		Text:       "Title: Lease\nCorrespondent: Landlord",
		Vector:     []float32{1, 0},
	}, ocr)
	if header.Text != "Title: Lease\nCorrespondent: Landlord" {
		t.Fatalf("header text = %q", header.Text)
	}
	if header.StartByte != 0 || header.EndByte != 0 {
		t.Fatalf("a header chunk must not claim document offsets: %+v", header)
	}

	// Offsets from a chunking of an older revision: the vector is still valid,
	// so the chunk is still indexed, but nothing is quoted from it.
	stale := chunkFrom(embedstore.Chunk{
		DocumentID: "doc1",
		Kind:       embedstore.KindBody,
		StartByte:  9000,
		EndByte:    9500,
		Vector:     []float32{1, 0},
	}, ocr)
	if stale.Text != "" {
		t.Fatalf("stale offsets produced a quote from nowhere: %q", stale.Text)
	}
	if len(stale.Vector) == 0 {
		t.Fatal("a stale-offset chunk must keep its vector")
	}
}

func TestSliceTextStaysOnRuneBoundaries(t *testing.T) {
	ocr := "Straße Übergabe"
	// One byte into the two-byte ß, and one byte into the Ü.
	got := sliceText(ocr, strings.Index(ocr, "ß")+1, strings.Index(ocr, "Ü")+1)
	if !strings.ContainsRune(got, 'ß') {
		t.Fatalf("a mid-rune start was not aligned back: %q", got)
	}
	if sliceText(ocr, 5, 5) != "" || sliceText(ocr, -1, 4) != "" {
		t.Fatal("an empty or negative range must resolve to nothing")
	}
}

func TestMatchesSpecRejectsOtherModelsAndLengths(t *testing.T) {
	spec := fulltext.VectorSpec{Model: "m", Dims: 2}
	ok := embedstore.Chunk{Model: "m", Dims: 2, Vector: []float32{1, 0}}
	if !matchesSpec(ok, spec) {
		t.Fatal("a matching chunk was rejected")
	}
	for name, row := range map[string]embedstore.Chunk{
		"other model":    {Model: "n", Dims: 2, Vector: []float32{1, 0}},
		"other dims":     {Model: "m", Dims: 3, Vector: []float32{1, 0, 0}},
		"short vector":   {Model: "m", Dims: 2, Vector: []float32{1}},
		"no vector left": {Model: "m", Dims: 2},
	} {
		if matchesSpec(row, spec) {
			t.Fatalf("%s should not be indexed: a wrong-length vector is dropped in silence", name)
		}
	}
}
