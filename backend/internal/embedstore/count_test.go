package embedstore

import "testing"

// The index compares this against its own document count to detect drift, so
// counting anything ForEachChunk would not walk means rebuilding every boot.
func TestCountChunksMatchesWhatForEachWalks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "text")
	insertDocument(t, db, "doc2", "text")

	if err := Replace(db, sampleState("doc1"), sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace doc1: %v", err)
	}
	other := sampleState("doc2")
	other.Model = "old-model"
	if err := Replace(db, other, sampleChunks("doc2")); err != nil {
		t.Fatalf("Replace doc2: %v", err)
	}

	walked := 0
	if err := ForEachChunk(db, "text-embedding-3-small", 4, func(Chunk) error {
		walked++
		return nil
	}); err != nil {
		t.Fatalf("ForEachChunk: %v", err)
	}

	got, err := CountChunks(db, "text-embedding-3-small", 4)
	if err != nil {
		t.Fatalf("CountChunks: %v", err)
	}
	if got != walked || got != 3 {
		t.Fatalf("CountChunks = %d, ForEachChunk walked %d", got, walked)
	}

	for _, tc := range []struct {
		model string
		dims  int
	}{
		{"text-embedding-3-small", 8},
		{"other-model", 4},
		{"", 4},
		{"text-embedding-3-small", 0},
	} {
		if n, err := CountChunks(db, tc.model, tc.dims); err != nil || n != 0 {
			t.Fatalf("CountChunks(%q, %d) = %d, %v", tc.model, tc.dims, n, err)
		}
	}
}
