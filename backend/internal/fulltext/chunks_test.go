package fulltext

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/chunk"
	"lemmary/backend/internal/retrieval"
)

// fakeChunkSource is the embedding store as the index sees it, without a
// database. The app handle is passed through untouched, which is what lets
// every test here run with none.
type fakeChunkSource struct {
	spec   VectorSpec
	off    bool
	chunks []Chunk
	err    error
}

func (f *fakeChunkSource) Spec(core.App) (VectorSpec, bool) {
	return f.spec, !f.off && f.spec.Valid()
}

func (f *fakeChunkSource) ForDocument(_ core.App, documentID string, spec VectorSpec) ([]Chunk, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := []Chunk{}
	for _, c := range f.chunks {
		if c.DocumentID == documentID && len(c.Vector) == spec.Dims {
			out = append(out, c)
		}
	}
	return out, nil
}

func (f *fakeChunkSource) ForEach(_ core.App, spec VectorSpec, fn func(Chunk) error) error {
	if f.err != nil {
		return f.err
	}
	// The real source scans in (document, ordinal) order and the index relies
	// on nothing else, but sorting here keeps the fake honest about it.
	ordered := make([]Chunk, 0, len(f.chunks))
	for _, c := range f.chunks {
		if len(c.Vector) == spec.Dims {
			ordered = append(ordered, c)
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].DocumentID != ordered[j].DocumentID {
			return ordered[i].DocumentID < ordered[j].DocumentID
		}
		return ordered[i].Ord < ordered[j].Ord
	})
	for _, c := range ordered {
		if err := fn(c); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeChunkSource) Count(_ core.App, spec VectorSpec) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	n := 0
	for _, c := range f.chunks {
		if len(c.Vector) == spec.Dims {
			n++
		}
	}
	return n, nil
}

// unit returns a normalised vector pointing mostly along axis, so two chunks on
// the same axis are close and two on different axes are not.
func unit(dims, axis int) []float32 {
	vec := make([]float32, dims)
	vec[axis%dims] = 1
	return vec
}

func testChunkSpec() VectorSpec { return VectorSpec{Model: "test-embed", Dims: 4} }

func chunkOf(documentID, userID string, ord, axis int, text string) Chunk {
	return Chunk{
		DocumentID: documentID,
		UserID:     userID,
		Ord:        ord,
		StartByte:  ord * 100,
		EndByte:    ord*100 + 40,
		Text:       text,
		Vector:     unit(4, axis),
	}
}

func testChunkIndex(t *testing.T, src *fakeChunkSource) *Index {
	t.Helper()
	idx := New()
	idx.SetChunkSource(src)
	if err := idx.Open(t.TempDir()); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if err := idx.SetVectorSpec(idx.SourceSpec(nil)); err != nil {
		t.Fatalf("set vector spec: %v", err)
	}
	return idx
}

func mustRebuildChunks(t *testing.T, idx *Index) int {
	t.Helper()
	n, err := idx.RebuildChunks(nil)
	if err != nil {
		t.Fatalf("rebuild chunks: %v", err)
	}
	return n
}

func searchChunks(t *testing.T, idx *Index, q retrieval.ChunkQuery) []retrieval.ChunkHit {
	t.Helper()
	hits, err := idx.SearchChunks(context.Background(), q)
	if err != nil {
		t.Fatalf("search chunks: %v", err)
	}
	return hits
}

func TestEncodeVectorBase64MatchesTheStoredLayout(t *testing.T) {
	vec := []float32{1, -0.5, 3.25}
	raw := make([]byte, 12)
	binary.LittleEndian.PutUint32(raw[0:], math.Float32bits(1))
	binary.LittleEndian.PutUint32(raw[4:], math.Float32bits(-0.5))
	binary.LittleEndian.PutUint32(raw[8:], math.Float32bits(3.25))
	if got, want := encodeVectorBase64(vec), base64.StdEncoding.EncodeToString(raw); got != want {
		t.Fatalf("encoding drifted from the stored blob layout: %q != %q", got, want)
	}
	if encodeVectorBase64(nil) != "" {
		t.Fatal("an empty vector must not produce a field value")
	}
}

func TestChunkDocIDRoundTrip(t *testing.T) {
	id := chunkDocID("abc123", 42)
	if id != "abc123:00042" {
		t.Fatalf("id = %q", id)
	}
	doc, ord, ok := splitChunkDocID(id)
	if !ok || doc != "abc123" || ord != 42 {
		t.Fatalf("split = %q, %d, %v", doc, ord, ok)
	}
	if _, _, ok := splitChunkDocID("no-ordinal"); ok {
		t.Fatal("an id without an ordinal must not parse")
	}
}

func TestRebuildLoadsEveryStoredChunk(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		chunkOf("doc1", "u1", 0, 0, "the annual heating bill"),
		chunkOf("doc1", "u1", 1, 1, "payment terms and bank details"),
		chunkOf("doc2", "u2", 0, 2, "a lease for a flat in Berlin"),
	}}
	idx := testChunkIndex(t, src)

	if !idx.ChunksReady() {
		t.Fatal("chunk index should be open for a valid spec")
	}
	if !idx.ChunksNeedRebuild() {
		t.Fatal("a freshly created chunk index should be flagged for filling")
	}
	if n := mustRebuildChunks(t, idx); n != 3 {
		t.Fatalf("indexed %d chunks, want 3", n)
	}
	if idx.ChunksNeedRebuild() {
		t.Fatal("the flag should clear once the index is filled")
	}
	if count, err := idx.ChunkCount(); err != nil || count != 3 {
		t.Fatalf("chunk count = %d, %v", count, err)
	}

	// A vector of the wrong length is dropped silently by Bleve, so the loader
	// must never hand one over: this is the source's filter doing its job.
	src.chunks = append(src.chunks, Chunk{DocumentID: "doc3", UserID: "u1", Vector: []float32{1, 0}})
	if n := mustRebuildChunks(t, idx); n != 3 {
		t.Fatalf("a wrong-length vector reached the index: indexed %d", n)
	}
}

func TestChunkSearchIsolatesUsers(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		chunkOf("mine", "u1", 0, 0, "the rent is 900 EUR a month"),
		chunkOf("theirs", "u2", 0, 0, "the rent is 700 EUR a month"),
	}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	hits := searchChunks(t, idx, retrieval.ChunkQuery{Vector: unit(4, 0), UserID: "u1", K: 10})
	if len(hits) != 1 || hits[0].DocumentID != "mine" {
		t.Fatalf("kNN crossed an owner boundary: %#v", hits)
	}

	// The same must hold for the keyword half and for the fused query.
	textHits := searchChunks(t, idx, retrieval.ChunkQuery{Text: "rent", UserID: "u1", K: 10})
	if len(textHits) != 1 || textHits[0].DocumentID != "mine" {
		t.Fatalf("chunk BM25 crossed an owner boundary: %#v", textHits)
	}
	bothHits := searchChunks(t, idx, retrieval.ChunkQuery{Vector: unit(4, 0), Text: "rent", UserID: "u1", K: 10})
	if len(bothHits) != 1 || bothHits[0].DocumentID != "mine" {
		t.Fatalf("hybrid chunk search crossed an owner boundary: %#v", bothHits)
	}
}

func TestChunkSearchReturnsStoredFields(t *testing.T) {
	chunk := chunkOf("doc1", "u1", 7, 0, "Die monatliche Kaltmiete beträgt 1234 EUR.")
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{chunk}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	hits := searchChunks(t, idx, retrieval.ChunkQuery{Vector: unit(4, 0), UserID: "u1", K: 5})
	if len(hits) != 1 {
		t.Fatalf("hits = %#v", hits)
	}
	got := hits[0]
	if got.DocumentID != "doc1" || got.Ord != 7 {
		t.Fatalf("identity did not survive storage: %+v", got)
	}
	if got.StartByte != chunk.StartByte || got.EndByte != chunk.EndByte {
		t.Fatalf("offsets did not survive storage: %+v", got)
	}
	if got.Text != chunk.Text {
		t.Fatalf("text did not survive storage: %q", got.Text)
	}
	// A kNN hit carries no fragments, so a score outside the cosine range
	// would mean the passage layer is ranking on something else entirely.
	if got.Score < 0 || got.Score > 1.0001 {
		t.Fatalf("cosine score out of range: %v", got.Score)
	}
}

// The stored copy is what retrieval quotes -- it is preferred over re-slicing
// the OCR column -- so the cap has to clear both of the chunker's ceilings. A
// cap below them silently ate the tail of every full-size body chunk and most
// of a header that rendered a summary, and nothing anywhere said so.
func TestChunkTextSurvivesAFullSizeChunkAndHeader(t *testing.T) {
	body := strings.Repeat("ä", chunk.DefaultOptions().MaxRunes)
	header := strings.Repeat("ö", chunk.HeaderMaxRunes)
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		chunkOf("doc1", "u1", 0, 0, header),
		chunkOf("doc1", "u1", 1, 1, body),
	}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	for _, tc := range []struct {
		name string
		axis int
		want string
	}{
		{"header", 0, header},
		{"body", 1, body},
	} {
		hits := searchChunks(t, idx, retrieval.ChunkQuery{Vector: unit(4, tc.axis), UserID: "u1", K: 5})
		if len(hits) == 0 || hits[0].Text != tc.want {
			got := ""
			if len(hits) > 0 {
				got = hits[0].Text
			}
			t.Fatalf("the %s chunk came back %d runes, want %d",
				tc.name, utf8.RuneCountInString(got), utf8.RuneCountInString(tc.want))
		}
	}
}

func TestChunkSearchRanksTheNearestVectorFirst(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		chunkOf("near", "u1", 0, 0, "alpha"),
		chunkOf("far", "u1", 0, 1, "beta"),
	}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	hits := searchChunks(t, idx, retrieval.ChunkQuery{Vector: unit(4, 0), UserID: "u1", K: 10})
	if len(hits) == 0 || hits[0].DocumentID != "near" {
		t.Fatalf("nearest vector did not rank first: %#v", hits)
	}
	for _, hit := range hits {
		if hit.Score < 0 || hit.Score > 1.0001 {
			t.Fatalf("cosine score out of range: %+v", hit)
		}
	}
}

func TestChunkSearchFusesTextAndVector(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		// Close to the query vector, says nothing about it.
		chunkOf("dense-only", "u1", 0, 0, "unrelated wording entirely"),
		// Far from the query vector, carries the word.
		chunkOf("lexical-only", "u1", 0, 2, "the Kaltmiete is stated here"),
	}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	hits := searchChunks(t, idx, retrieval.ChunkQuery{
		Vector: unit(4, 0),
		Text:   "Kaltmiete",
		UserID: "u1",
		K:      10,
	})
	found := map[string]bool{}
	for _, hit := range hits {
		found[hit.DocumentID] = true
	}
	if !found["dense-only"] || !found["lexical-only"] {
		t.Fatalf("fusion lost one of the two signals: %#v", hits)
	}
}

func TestChunkSearchFiltersByDocumentIDs(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		chunkOf("doc1", "u1", 0, 0, "first"),
		chunkOf("doc2", "u1", 0, 0, "second"),
		chunkOf("doc3", "u1", 0, 0, "third"),
	}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	hits := searchChunks(t, idx, retrieval.ChunkQuery{
		Vector:      unit(4, 0),
		UserID:      "u1",
		DocumentIDs: []string{"doc1", "doc3"},
		K:           10,
	})
	if len(hits) != 2 {
		t.Fatalf("expected the two eligible documents, got %#v", hits)
	}
	for _, hit := range hits {
		if hit.DocumentID == "doc2" {
			t.Fatalf("a filtered-out document was returned: %#v", hits)
		}
	}
}

func TestChunkSearchRejectsAWrongLengthVector(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{chunkOf("doc1", "u1", 0, 0, "text")}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	_, err := idx.SearchChunks(context.Background(), retrieval.ChunkQuery{
		Vector: []float32{1, 0, 0},
		UserID: "u1",
	})
	if !errors.Is(err, ErrVectorDims) {
		t.Fatalf("a wrong-length query vector must be refused, got %v", err)
	}
}

func TestChunkSearchIsSilentWithoutAnIndex(t *testing.T) {
	idx := testIndex(t)
	hits, err := idx.SearchChunks(context.Background(), retrieval.ChunkQuery{Text: "anything"})
	if err != nil || len(hits) != 0 {
		t.Fatalf("an unconfigured chunk index must degrade quietly: %v, %#v", err, hits)
	}
}

func TestVectorSpecChangeWipesOnlyTheChunkIndex(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		chunkOf("doc1", "u1", 0, 0, "the rent is 900 EUR"),
	}}
	idx := testChunkIndex(t, src)
	mustPut(t, idx, "doc1", map[string]any{
		FieldUser: "u1", FieldTitle: "Lease", FieldAll: "Lease rent",
	})
	mustRebuildChunks(t, idx)
	if count, _ := idx.ChunkCount(); count != 1 {
		t.Fatalf("chunk count = %d", count)
	}

	// A different model at the same dimensions is still a different index: the
	// two models' vectors mean nothing to each other.
	next := VectorSpec{Model: "other-embed", Dims: 4}
	src.spec = next
	if err := idx.SetVectorSpec(next, true); err != nil {
		t.Fatalf("set vector spec: %v", err)
	}
	if count, err := idx.ChunkCount(); err != nil || count != 0 {
		t.Fatalf("the chunk index should have been wiped: %d, %v", count, err)
	}
	if !idx.ChunksNeedRebuild() {
		t.Fatal("a wiped chunk index should ask to be refilled")
	}
	if hits := searchIDs(t, idx, Query{Text: "lease", UserID: "u1"}); !containsID(hits, "doc1") {
		t.Fatalf("the documents index must survive a model switch, got %v", hits)
	}

	mustRebuildChunks(t, idx)
	version, err := os.ReadFile(filepath.Join(idx.chunkVersionPath))
	if err != nil {
		t.Fatalf("version file: %v", err)
	}
	if got := strings.TrimSpace(string(version)); got != next.version() {
		t.Fatalf("version file = %q, want %q", got, next.version())
	}
}

func TestVectorSpecOffRemovesTheChunkDirectory(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{chunkOf("doc1", "u1", 0, 0, "text")}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	if err := idx.SetVectorSpec(VectorSpec{}, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if idx.ChunksReady() {
		t.Fatal("chunk search should be off")
	}
	if _, err := os.Stat(idx.chunkPath); !os.IsNotExist(err) {
		t.Fatalf("the chunk directory should be gone, stat says %v", err)
	}
	// Dims of 0 is the same state by another route: a model is configured but
	// no provider has answered yet.
	if err := idx.SetVectorSpec(VectorSpec{Model: "test-embed"}, true); err != nil {
		t.Fatalf("disable by dims: %v", err)
	}
	if idx.ChunksReady() {
		t.Fatal("a spec without dimensions must not open an index")
	}
}

func TestChunkUpsertDropsStaleOrdinals(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		chunkOf("doc1", "u1", 0, 0, "first half"),
		chunkOf("doc1", "u1", 1, 1, "second half"),
		chunkOf("doc1", "u1", 2, 2, "third half"),
	}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)
	if count, _ := idx.ChunkCount(); count != 3 {
		t.Fatalf("chunk count = %d", count)
	}

	// Re-embedded after an edit: shorter text, so the tail ordinals describe
	// passages that no longer exist.
	src.chunks = []Chunk{chunkOf("doc1", "u1", 0, 0, "the whole thing, rewritten")}
	if err := idx.upsertChunksUnlocked(nil, "doc1"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if count, err := idx.ChunkCount(); err != nil || count != 1 {
		t.Fatalf("stale ordinals survived the upsert: %d, %v", count, err)
	}
	hits := searchChunks(t, idx, retrieval.ChunkQuery{Text: "rewritten", UserID: "u1", K: 5})
	if len(hits) != 1 || hits[0].Ord != 0 {
		t.Fatalf("the replacement chunk is not searchable: %#v", hits)
	}
}

func TestDocumentDeleteRemovesItsChunks(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec(), chunks: []Chunk{
		chunkOf("doc1", "u1", 0, 0, "keep me"),
		chunkOf("doc2", "u1", 0, 1, "delete me"),
		chunkOf("doc2", "u1", 1, 2, "delete me too"),
	}}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	idx.EnqueueDelete("doc2")
	idx.WaitIdle()

	if count, err := idx.ChunkCount(); err != nil || count != 1 {
		t.Fatalf("a deleted document left its passages behind: %d, %v", count, err)
	}
	hits := searchChunks(t, idx, retrieval.ChunkQuery{Vector: unit(4, 1), UserID: "u1", K: 10})
	for _, hit := range hits {
		if hit.DocumentID == "doc2" {
			t.Fatalf("deleted document still searchable: %#v", hits)
		}
	}
}

func TestChunksReplacedReindexesThroughTheQueue(t *testing.T) {
	src := &fakeChunkSource{spec: testChunkSpec()}
	idx := testChunkIndex(t, src)
	mustRebuildChunks(t, idx)

	src.chunks = []Chunk{chunkOf("doc1", "u1", 0, 0, "a brand new passage")}
	// The listener call the embedding store makes after its transaction
	// commits. The app handle is only passed through to the source, which in
	// this test does not need one.
	idx.ChunksReplaced(nil, "doc1")
	idx.WaitIdle()

	hits := searchChunks(t, idx, retrieval.ChunkQuery{Text: "passage", UserID: "u1", K: 5})
	if len(hits) != 1 || hits[0].DocumentID != "doc1" {
		t.Fatalf("the listener did not reach the index: %#v", hits)
	}
}
