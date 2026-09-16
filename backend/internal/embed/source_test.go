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

	// A vector field cannot be built before the dimension count is known.
	if _, ok := SpecFrom(config.Config{
		EmbeddingProvider: provider,
		EmbeddingModel:    "text-embedding-3-small",
	}); ok {
		t.Fatal("a spec with no dimensions must not open an index")
	}

	if _, ok := SpecFrom(config.Config{EmbeddingDims: 1536}); ok {
		t.Fatal("no embedding binding must report off")
	}
}

func TestChunkFromResolvesTextFromTheStoredOffsets(t *testing.T) {
	ocr := "Die monatliche Kaltmiete beträgt 1234 EUR. Die Kaution beträgt 3702 EUR."
	start := strings.Index(ocr, "Die Kaution")

	body := chunkFrom(embedstore.Chunk{
		DocumentID: "doc1",
		UserID:     "u1",
		Ordinal:    2,
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

	// Offsets from an older revision: still indexed, but nothing quoted from it.
	stale := chunkFrom(embedstore.Chunk{
		DocumentID: "doc1",
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
