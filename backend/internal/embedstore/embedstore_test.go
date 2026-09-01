package embedstore

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	_ "modernc.org/sqlite"
)

// openTestDB gives the store a real SQLite file with the pragmas PocketBase
// uses, because the store speaks SQL directly: a mock would only prove that the
// strings in this package match the strings in its tests.
func openTestDB(t *testing.T) *dbx.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.db")
	pragmas := "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := dbx.Open("sqlite", path+pragmas)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// The candidate and orphan queries join `documents`, so the tests need one.
	_, err = db.NewQuery(`CREATE TABLE documents (
		id TEXT PRIMARY KEY,
		user TEXT NOT NULL DEFAULT '',
		ocr_text TEXT NOT NULL DEFAULT '',
		duplicate_of TEXT NOT NULL DEFAULT '',
		processing_status TEXT NOT NULL DEFAULT 'completed',
		created TEXT NOT NULL DEFAULT ''
	)`).Execute()
	if err != nil {
		t.Fatalf("create documents: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema: %v", err)
	}
	return db
}

func insertDocument(t *testing.T, db *dbx.DB, id, ocrText string, opts ...func(dbx.Params)) {
	t.Helper()
	params := dbx.Params{
		"id": id, "user": "user1", "ocr_text": ocrText,
		"duplicate_of": "", "processing_status": "completed", "created": id,
	}
	for _, opt := range opts {
		opt(params)
	}
	_, err := db.Insert("documents", params).Execute()
	if err != nil {
		t.Fatalf("insert document %s: %v", id, err)
	}
}

func vector(n int, seed float32) []float32 {
	out := make([]float32, n)
	for i := range out {
		out[i] = seed + float32(i)/8
	}
	return out
}

func sampleState(id string) State {
	return State{
		DocumentID:     id,
		UserID:         "user1",
		Model:          "text-embedding-3-small",
		Dims:           4,
		ChunkerVersion: 1,
		TextHash:       TextHash("body"),
		HeaderHash:     TextHash("header"),
		Status:         StatusOK,
	}
}

func sampleChunks(id string) []Chunk {
	return []Chunk{
		{DocumentID: id, Ordinal: 0, Kind: KindHeader, Text: "Title: Invoice", Vector: vector(4, 1)},
		{DocumentID: id, Ordinal: 1, Kind: KindBody, StartByte: 0, EndByte: 12, Vector: vector(4, 2)},
		{DocumentID: id, Ordinal: 2, Kind: KindBody, StartByte: 10, EndByte: 22, Vector: vector(4, 3)},
	}
}

// The BLOB layout is the contract with the vector index, which decodes the same
// bytes as little-endian float32. A round trip through SQLite is the only place
// that is actually proven.
func TestVectorRoundTripsThroughSQLite(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "some text")

	want := sampleChunks("doc1")
	if err := Replace(db, sampleState("doc1"), want); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	got, err := Chunks(db, "doc1")
	if err != nil {
		t.Fatalf("Chunks: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("got %d chunks, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].Ordinal != want[i].Ordinal || got[i].Kind != want[i].Kind {
			t.Fatalf("chunk %d = %+v", i, got[i])
		}
		if len(got[i].Vector) != len(want[i].Vector) {
			t.Fatalf("chunk %d has %d dims, want %d", i, len(got[i].Vector), len(want[i].Vector))
		}
		for j := range got[i].Vector {
			if got[i].Vector[j] != want[i].Vector[j] {
				t.Fatalf("chunk %d value %d = %v, want %v", i, j, got[i].Vector[j], want[i].Vector[j])
			}
		}
	}
	if got[0].Text != "Title: Invoice" {
		t.Fatalf("header text = %q", got[0].Text)
	}
	if got[1].Text != "" {
		t.Fatalf("body chunks store offsets, not text: %q", got[1].Text)
	}
}

func TestDecodeVectorRejectsCorruptLength(t *testing.T) {
	t.Parallel()
	if got := DecodeVector([]byte{1, 2, 3}); got != nil {
		t.Fatalf("DecodeVector(3 bytes) = %v, want nil", got)
	}
	if got := DecodeVector(nil); got != nil {
		t.Fatalf("DecodeVector(nil) = %v, want nil", got)
	}
}

// A re-embed after an edit produces a different number of chunks; the previous
// tail must not survive as passages that no longer exist.
func TestReplaceDropsThePreviousChunks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "some text")

	if err := Replace(db, sampleState("doc1"), sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	shorter := []Chunk{{DocumentID: "doc1", Ordinal: 0, Kind: KindHeader, Text: "h", Vector: vector(4, 9)}}
	if err := Replace(db, sampleState("doc1"), shorter); err != nil {
		t.Fatalf("Replace again: %v", err)
	}

	got, err := Chunks(db, "doc1")
	if err != nil {
		t.Fatalf("Chunks: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d chunks after a shorter replace, want 1", len(got))
	}
	state, ok, err := Get(db, "doc1")
	if err != nil || !ok {
		t.Fatalf("Get: %v ok=%v", err, ok)
	}
	if state.ChunkCount != 1 {
		t.Fatalf("chunk_count = %d, want 1", state.ChunkCount)
	}
}

// A vector of the wrong length is dropped silently by the vector index, so a
// document holding one would look embedded and never be findable.
func TestReplaceRefusesMixedDimensions(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "some text")

	chunks := []Chunk{{DocumentID: "doc1", Ordinal: 0, Kind: KindHeader, Text: "h", Vector: vector(8, 1)}}
	if err := Replace(db, sampleState("doc1"), chunks); err == nil {
		t.Fatal("Replace accepted an 8-dimension vector for a 4-dimension state")
	}
}

func TestReplaceIsUpsert(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "some text")

	state := sampleState("doc1")
	if err := Replace(db, state, sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	state.TextHash = TextHash("edited")
	state.Truncated = true
	if err := Replace(db, state, sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace again: %v", err)
	}

	got, ok, err := Get(db, "doc1")
	if err != nil || !ok {
		t.Fatalf("Get: %v ok=%v", err, ok)
	}
	if got.TextHash != TextHash("edited") || !got.Truncated {
		t.Fatalf("state was not updated: %+v", got)
	}
}

func TestGetReportsMissingWithoutAnError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	state, ok, err := Get(db, "nope")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if ok {
		t.Fatalf("Get reported a state for an unknown document: %+v", state)
	}
}

func TestMarkFailedCountsAttemptsAndKeepsChunks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "some text")
	if err := Replace(db, sampleState("doc1"), sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	next := time.Now().Add(10 * time.Minute)
	for i := 1; i <= 2; i++ {
		if err := MarkFailed(db, "doc1", "user1", errors.New("provider is down"), next); err != nil {
			t.Fatalf("MarkFailed: %v", err)
		}
		state, _, err := Get(db, "doc1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if state.Attempts != i {
			t.Fatalf("attempts = %d after %d failures", state.Attempts, i)
		}
		if state.Status != StatusFailed || state.LastError == "" || state.NextAttemptAt == "" {
			t.Fatalf("failure not recorded: %+v", state)
		}
	}

	// A provider outage degrades retrieval to what it was, it does not delete
	// the document out of the dense index.
	chunks, err := Chunks(db, "doc1")
	if err != nil || len(chunks) != 3 {
		t.Fatalf("chunks after failures: %d (%v)", len(chunks), err)
	}
}

func TestMarkFailedCreatesARowForANeverEmbeddedDocument(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "some text")

	if err := MarkFailed(db, "doc1", "user1", errors.New("boom"), time.Now()); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	state, ok, err := Get(db, "doc1")
	if err != nil || !ok {
		t.Fatalf("Get: %v ok=%v", err, ok)
	}
	if state.Attempts != 1 || state.Status != StatusFailed {
		t.Fatalf("state = %+v", state)
	}
}

func TestCandidatesCoversEveryReasonToReEmbed(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	now := time.Now()
	const model = "text-embedding-3-small"

	insertDocument(t, db, "doc-fresh", "text")
	insertDocument(t, db, "doc-missing", "text")
	insertDocument(t, db, "doc-stale", "text")
	insertDocument(t, db, "doc-other-model", "text")
	insertDocument(t, db, "doc-other-dims", "text")
	insertDocument(t, db, "doc-old-chunker", "text")
	insertDocument(t, db, "doc-failed-due", "text")
	insertDocument(t, db, "doc-failed-backoff", "text")
	insertDocument(t, db, "doc-empty", "")
	insertDocument(t, db, "doc-duplicate", "text", func(p dbx.Params) { p["duplicate_of"] = "doc-fresh" })
	insertDocument(t, db, "doc-processing", "text", func(p dbx.Params) { p["processing_status"] = "processing" })
	insertDocument(t, db, "doc-pending", "text", func(p dbx.Params) { p["processing_status"] = "pending" })

	write := func(id string, mutate func(*State)) {
		state := sampleState(id)
		state.Model = model
		if mutate != nil {
			mutate(&state)
		}
		if err := Replace(db, state, []Chunk{{DocumentID: id, Ordinal: 0, Kind: KindHeader, Text: "h", Vector: vector(4, 1)}}); err != nil {
			t.Fatalf("Replace %s: %v", id, err)
		}
	}
	write("doc-fresh", nil)
	write("doc-stale", func(s *State) { s.Stale = true })
	write("doc-other-model", func(s *State) { s.Model = "other-model" })
	write("doc-old-chunker", func(s *State) { s.ChunkerVersion = 0 })
	write("doc-empty", nil)
	write("doc-duplicate", nil)
	write("doc-processing", nil)
	write("doc-pending", nil)

	// Dimensions are stored per chunk from the vector itself, so a dims change
	// has to be written straight onto the state row.
	write("doc-other-dims", nil)
	if _, err := db.NewQuery(`UPDATE document_embeddings SET dims = 8 WHERE document_id = 'doc-other-dims'`).Execute(); err != nil {
		t.Fatalf("update dims: %v", err)
	}

	if err := MarkFailed(db, "doc-failed-due", "user1", errors.New("x"), now.Add(-time.Minute)); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}
	if err := MarkFailed(db, "doc-failed-backoff", "user1", errors.New("x"), now.Add(time.Hour)); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	got, err := Candidates(db, model, 4, 1, 100, now)
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	want := map[string]bool{
		"doc-missing": true, "doc-stale": true, "doc-other-model": true,
		"doc-other-dims": true, "doc-old-chunker": true, "doc-failed-due": true,
	}
	if len(got) != len(want) {
		t.Fatalf("candidates = %v, want exactly %v", got, keys(want))
	}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected candidate %q (all: %v)", id, got)
		}
	}
}

func TestCandidatesIgnoresDimsBeforeTheFirstResponse(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "text")
	state := sampleState("doc1")
	if err := Replace(db, state, []Chunk{{DocumentID: "doc1", Ordinal: 0, Kind: KindHeader, Text: "h", Vector: vector(4, 1)}}); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	// dims 0 means "not recorded yet", which must not read as "every stored
	// row has the wrong length".
	got, err := Candidates(db, state.Model, 0, 1, 10, time.Now())
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("candidates = %v, want none", got)
	}
}

func TestCandidatesReturnsNothingWithoutAModel(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "text")

	got, err := Candidates(db, "  ", 0, 1, 10, time.Now())
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("candidates = %v, want none when the feature is off", got)
	}
}

func TestCandidatesRespectsTheLimit(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	for i := 0; i < 5; i++ {
		insertDocument(t, db, fmt.Sprintf("doc%d", i), "text")
	}

	got, err := Candidates(db, "m", 0, 1, 2, time.Now())
	if err != nil {
		t.Fatalf("Candidates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2", len(got))
	}
}

func TestDeleteRemovesBothTables(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "text")
	if err := Replace(db, sampleState("doc1"), sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if err := Delete(db, "doc1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if chunks, err := Chunks(db, "doc1"); err != nil || len(chunks) != 0 {
		t.Fatalf("chunks = %d (%v)", len(chunks), err)
	}
	if _, ok, err := Get(db, "doc1"); err != nil || ok {
		t.Fatalf("state still present: ok=%v err=%v", ok, err)
	}
}

// The record hook cannot run for a document deleted while the feature was off,
// or for one that vanished with a restored backup.
func TestDeleteOrphansSweepsRowsWithNoDocument(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "kept", "text")
	insertDocument(t, db, "gone", "text")
	if err := Replace(db, sampleState("kept"), sampleChunks("kept")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if err := Replace(db, sampleState("gone"), sampleChunks("gone")); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := db.Delete("documents", dbx.HashExp{"id": "gone"}).Execute(); err != nil {
		t.Fatalf("delete document: %v", err)
	}

	n, err := DeleteOrphans(db)
	if err != nil {
		t.Fatalf("DeleteOrphans: %v", err)
	}
	if n != 1 {
		t.Fatalf("DeleteOrphans removed %d states, want 1", n)
	}
	if chunks, err := Chunks(db, "gone"); err != nil || len(chunks) != 0 {
		t.Fatalf("orphan chunks survived: %d (%v)", len(chunks), err)
	}
	if chunks, err := Chunks(db, "kept"); err != nil || len(chunks) != 3 {
		t.Fatalf("live chunks were swept: %d (%v)", len(chunks), err)
	}
}

func TestMarkStaleFlagsWithoutDeleting(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "text")
	if err := Replace(db, sampleState("doc1"), sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if err := MarkStale(db, "doc1"); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	state, _, err := Get(db, "doc1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !state.Stale {
		t.Fatal("document was not marked stale")
	}
	if chunks, err := Chunks(db, "doc1"); err != nil || len(chunks) != 3 {
		t.Fatalf("stale chunks should stay readable: %d (%v)", len(chunks), err)
	}
}

// A rebuild after a model switch must not silently pick up the previous
// model's vectors: the index drops the wrong length without a word.
func TestForEachChunkFiltersByModelAndDims(t *testing.T) {
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

	var seen []string
	err := ForEachChunk(db, "text-embedding-3-small", 4, func(c Chunk) error {
		seen = append(seen, fmt.Sprintf("%s:%d", c.DocumentID, c.Ordinal))
		if len(c.Vector) != 4 {
			t.Fatalf("chunk %s has %d dims", c.DocumentID, len(c.Vector))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachChunk: %v", err)
	}
	if len(seen) != 3 {
		t.Fatalf("visited %v, want only doc1's three chunks", seen)
	}
}

func TestForEachChunkPagesThroughEverything(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	total := 0
	for d := 0; d < 4; d++ {
		id := fmt.Sprintf("doc%d", d)
		insertDocument(t, db, id, "text")
		chunks := make([]Chunk, 0, 400)
		for i := 0; i < 400; i++ {
			chunks = append(chunks, Chunk{DocumentID: id, Ordinal: i, Kind: KindBody, Vector: vector(4, float32(i))})
		}
		if err := Replace(db, sampleState(id), chunks); err != nil {
			t.Fatalf("Replace: %v", err)
		}
		total += len(chunks)
	}

	seen := 0
	prev := ""
	prevOrd := -1
	err := ForEachChunk(db, "text-embedding-3-small", 4, func(c Chunk) error {
		seen++
		if c.DocumentID < prev || (c.DocumentID == prev && c.Ordinal <= prevOrd) {
			return fmt.Errorf("out of order at %s:%d after %s:%d", c.DocumentID, c.Ordinal, prev, prevOrd)
		}
		prev, prevOrd = c.DocumentID, c.Ordinal
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachChunk: %v", err)
	}
	if seen != total {
		t.Fatalf("visited %d chunks, want %d", seen, total)
	}
}

func TestForEachChunkStopsOnCallbackError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "text")
	if err := Replace(db, sampleState("doc1"), sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	sentinel := errors.New("stop")
	err := ForEachChunk(db, "text-embedding-3-small", 4, func(Chunk) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatalf("ForEachChunk err = %v, want the callback's", err)
	}
}

func TestLoadStatsCountsTheBacklog(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	now := time.Now()
	const model = "text-embedding-3-small"

	insertDocument(t, db, "doc-ok", "text")
	insertDocument(t, db, "doc-stale", "text")
	insertDocument(t, db, "doc-failed", "text")
	insertDocument(t, db, "doc-new", "text")
	insertDocument(t, db, "doc-empty", "")

	for _, id := range []string{"doc-ok", "doc-stale"} {
		if err := Replace(db, sampleState(id), sampleChunks(id)); err != nil {
			t.Fatalf("Replace %s: %v", id, err)
		}
	}
	if err := MarkStale(db, "doc-stale"); err != nil {
		t.Fatalf("MarkStale: %v", err)
	}
	if err := MarkFailed(db, "doc-failed", "user1", errors.New("x"), now.Add(-time.Minute)); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	stats, err := LoadStats(db, model, 4, 1, now)
	if err != nil {
		t.Fatalf("LoadStats: %v", err)
	}
	if !stats.Enabled || stats.Model != model || stats.Dims != 4 {
		t.Fatalf("stats header = %+v", stats)
	}
	if stats.Total != 4 {
		t.Fatalf("total = %d, want 4 embeddable documents", stats.Total)
	}
	if stats.Embedded != 1 {
		t.Fatalf("embedded = %d, want 1", stats.Embedded)
	}
	if stats.Stale != 1 || stats.Failed != 1 {
		t.Fatalf("stale=%d failed=%d, want 1/1", stats.Stale, stats.Failed)
	}
	if stats.Chunks != 6 {
		t.Fatalf("chunks = %d, want 6", stats.Chunks)
	}
	// doc-new, doc-stale and doc-failed are all due.
	if stats.Pending != 3 {
		t.Fatalf("pending = %d, want 3", stats.Pending)
	}
}

func TestLoadStatsReportsDisabledWithoutAModel(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	stats, err := LoadStats(db, "", 0, 1, time.Now())
	if err != nil {
		t.Fatalf("LoadStats: %v", err)
	}
	if stats.Enabled {
		t.Fatalf("stats = %+v, want disabled", stats)
	}
}

func TestDropSchemaRemovesBothTables(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := DropSchema(db); err != nil {
		t.Fatalf("DropSchema: %v", err)
	}
	if _, err := Chunks(db, "doc1"); err == nil {
		t.Fatal("the chunks table still exists after DropSchema")
	}
	// Idempotent, so a down migration can run twice.
	if err := DropSchema(db); err != nil {
		t.Fatalf("second DropSchema: %v", err)
	}
}

func TestEnsureSchemaIsIdempotent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertDocument(t, db, "doc1", "text")
	if err := Replace(db, sampleState("doc1"), sampleChunks("doc1")); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema again: %v", err)
	}
	if chunks, err := Chunks(db, "doc1"); err != nil || len(chunks) != 3 {
		t.Fatalf("re-running EnsureSchema lost data: %d (%v)", len(chunks), err)
	}
}

func TestTextHashSeparatesFields(t *testing.T) {
	t.Parallel()
	if TextHash("ab", "c") == TextHash("a", "bc") {
		t.Fatal("field boundaries must change the hash")
	}
	if TextHash("a", "b") != TextHash("a", "b") {
		t.Fatal("TextHash is not stable")
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
