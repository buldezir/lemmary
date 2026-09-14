package migrations

import (
	"path/filepath"
	"testing"

	"github.com/pocketbase/dbx"
	_ "modernc.org/sqlite"

	"lemmary/backend/internal/embedstore"
)

// openLegacyEmbedDB builds the embedding tables as they were before the header
// chunk was dropped. Written out by hand rather than taken from
// embedstore.EnsureSchema on purpose: EnsureSchema now creates the *new* shape,
// and a migration that is only ever tested against the shape it produces proves
// nothing about the archives it has to convert.
func openLegacyEmbedDB(t *testing.T) *dbx.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.db")
	db, err := dbx.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	statements := []string{
		`CREATE TABLE document_embeddings (
			document_id     TEXT PRIMARY KEY,
			user            TEXT NOT NULL DEFAULT '',
			model           TEXT NOT NULL DEFAULT '',
			dims            INTEGER NOT NULL DEFAULT 0,
			chunker_version INTEGER NOT NULL DEFAULT 0,
			text_hash       TEXT NOT NULL DEFAULT '',
			header_hash     TEXT NOT NULL DEFAULT '',
			chunk_count     INTEGER NOT NULL DEFAULT 0,
			truncated       INTEGER NOT NULL DEFAULT 0,
			status          TEXT NOT NULL DEFAULT 'ok',
			stale           INTEGER NOT NULL DEFAULT 0,
			attempts        INTEGER NOT NULL DEFAULT 0,
			next_attempt_at TEXT NOT NULL DEFAULT '',
			last_error      TEXT NOT NULL DEFAULT '',
			embedded_at     TEXT NOT NULL DEFAULT ''
		) WITHOUT ROWID`,
		`CREATE INDEX idx_document_embeddings_state
			ON document_embeddings (status, stale, next_attempt_at)`,
		`CREATE TABLE document_chunks (
			document_id TEXT NOT NULL,
			ordinal     INTEGER NOT NULL,
			user        TEXT NOT NULL DEFAULT '',
			kind        TEXT NOT NULL DEFAULT 'body',
			start_byte  INTEGER NOT NULL DEFAULT 0,
			end_byte    INTEGER NOT NULL DEFAULT 0,
			text        TEXT NOT NULL DEFAULT '',
			model       TEXT NOT NULL DEFAULT '',
			dims        INTEGER NOT NULL DEFAULT 0,
			vector      BLOB NOT NULL,
			PRIMARY KEY (document_id, ordinal)
		) WITHOUT ROWID`,
		`CREATE INDEX idx_document_chunks_user ON document_chunks (user)`,
	}
	for _, sql := range statements {
		if _, err := db.NewQuery(sql).Execute(); err != nil {
			t.Fatalf("legacy schema: %v", err)
		}
	}
	return db
}

func insertLegacyChunk(t *testing.T, db *dbx.DB, ordinal int, kind string, vector []byte) {
	t.Helper()
	_, err := db.Insert("document_chunks", dbx.Params{
		"document_id": "doc1", "ordinal": ordinal, "user": "user1",
		"kind": kind, "start_byte": ordinal * 100, "end_byte": ordinal*100 + 50,
		"text": "", "model": "embed-1", "dims": 2, "vector": vector,
	}).Execute()
	if err != nil {
		t.Fatalf("insert chunk %d: %v", ordinal, err)
	}
}

func TestDropHeaderChunksKeepsTheBodyAndItsVectors(t *testing.T) {
	db := openLegacyEmbedDB(t)

	insertLegacyChunk(t, db, 0, "header", []byte{1, 2, 3, 4})
	insertLegacyChunk(t, db, 1, "body", []byte{5, 6, 7, 8})
	insertLegacyChunk(t, db, 2, "body", []byte{9, 10, 11, 12})
	_, err := db.Insert("document_embeddings", dbx.Params{
		"document_id": "doc1", "user": "user1", "model": "embed-1", "dims": 2,
		"chunker_version": 1, "text_hash": "abc", "header_hash": "def",
		"chunk_count": 3, "status": "ok",
	}).Execute()
	if err != nil {
		t.Fatalf("insert state: %v", err)
	}

	if err := dropHeaderChunks(db); err != nil {
		t.Fatalf("dropHeaderChunks: %v", err)
	}

	type row struct {
		Ordinal   int    `db:"ordinal"`
		StartByte int    `db:"start_byte"`
		Vector    []byte `db:"vector"`
	}
	var rows []row
	err = db.NewQuery(`SELECT ordinal, start_byte, vector FROM document_chunks
		WHERE document_id = 'doc1' ORDER BY ordinal`).All(&rows)
	if err != nil {
		t.Fatalf("read chunks: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("kept %d chunks, want the two body ones", len(rows))
	}
	// The body chunks keep their ordinals: they are the identity the Bleve
	// index and the passage layer address a passage by, and renumbering them
	// would orphan every indexed document.
	if rows[0].Ordinal != 1 || rows[1].Ordinal != 2 {
		t.Errorf("ordinals were renumbered: %d, %d", rows[0].Ordinal, rows[1].Ordinal)
	}
	if rows[0].StartByte != 100 || string(rows[0].Vector) != string([]byte{5, 6, 7, 8}) {
		t.Errorf("body chunk 1 changed: %+v", rows[0])
	}

	var count int
	err = db.NewQuery(`SELECT chunk_count FROM document_embeddings WHERE document_id = 'doc1'`).Row(&count)
	if err != nil {
		t.Fatalf("read chunk_count: %v", err)
	}
	if count != 2 {
		t.Errorf("chunk_count is %d, want 2", count)
	}

	for _, sql := range []string{
		`SELECT kind FROM document_chunks`,
		`SELECT text FROM document_chunks`,
		`SELECT header_hash FROM document_embeddings`,
	} {
		if _, err := db.NewQuery(sql).Execute(); err == nil {
			t.Errorf("%q still works; the column should be gone", sql)
		}
	}

	// The rebuild drops the old table, which takes its index with it. Losing
	// the user index would leave every chunk scan unindexed and say nothing.
	var indexes []string
	err = db.NewQuery(`SELECT name FROM sqlite_master
		WHERE type = 'index' AND tbl_name = 'document_chunks'`).Column(&indexes)
	if err != nil {
		t.Fatalf("read indexes: %v", err)
	}
	found := false
	for _, name := range indexes {
		if name == "idx_document_chunks_user" {
			found = true
		}
	}
	if !found {
		t.Errorf("idx_document_chunks_user did not survive the rebuild: %v", indexes)
	}
}

// A second run has to be harmless: migrations are not the only thing that can
// re-enter here, and a fresh install reaches this with the columns never having
// existed. The second call is the fresh-install path, taken through the same
// schema check.
func TestDropHeaderChunksIsIdempotent(t *testing.T) {
	db := openLegacyEmbedDB(t)
	insertLegacyChunk(t, db, 0, "body", []byte{1, 2, 3, 4})

	if err := dropHeaderChunks(db); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := dropHeaderChunks(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

func TestRestoreHeaderChunkColumnsPutsThemBackEmpty(t *testing.T) {
	db := openLegacyEmbedDB(t)
	insertLegacyChunk(t, db, 1, "body", []byte{1, 2, 3, 4})
	if err := dropHeaderChunks(db); err != nil {
		t.Fatalf("dropHeaderChunks: %v", err)
	}

	if err := restoreHeaderChunkColumns(db); err != nil {
		t.Fatalf("restoreHeaderChunkColumns: %v", err)
	}
	var kind string
	err := db.NewQuery(`SELECT kind FROM document_chunks WHERE ordinal = 1`).Row(&kind)
	if err != nil {
		t.Fatalf("read kind: %v", err)
	}
	if kind != "body" {
		t.Errorf("restored kind is %q, want the default %q", kind, "body")
	}
}

// The store has to work against a database that came through the migration,
// not only against one EnsureSchema built from scratch. This is the case that
// pins the two together: if the rebuilt table and the new shape ever drift, a
// Replace on an upgraded archive fails and nothing else here would say so.
func TestTheStoreWritesAndReadsAMigratedTable(t *testing.T) {
	db := openLegacyEmbedDB(t)
	insertLegacyChunk(t, db, 0, "header", []byte{1, 2, 3, 4})
	insertLegacyChunk(t, db, 1, "body", []byte{5, 6, 7, 8})
	_, err := db.Insert("document_embeddings", dbx.Params{
		"document_id": "doc1", "user": "user1", "model": "embed-1", "dims": 2,
		"chunker_version": 1, "text_hash": "abc", "header_hash": "def",
		"chunk_count": 2, "status": "ok",
	}).Execute()
	if err != nil {
		t.Fatalf("insert state: %v", err)
	}

	if err := dropHeaderChunks(db); err != nil {
		t.Fatalf("dropHeaderChunks: %v", err)
	}

	// EnsureSchema is idempotent and must find nothing left to do.
	if err := embedstore.EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema over a migrated table: %v", err)
	}

	state := embedstore.State{
		DocumentID: "doc1", UserID: "user1", Model: "embed-1", Dims: 2,
		ChunkerVersion: 1, TextHash: "xyz", Status: embedstore.StatusOK,
	}
	chunks := []embedstore.Chunk{
		{DocumentID: "doc1", Ordinal: 0, StartByte: 0, EndByte: 12, Vector: []float32{1, 0}},
		{DocumentID: "doc1", Ordinal: 1, StartByte: 10, EndByte: 22, Vector: []float32{0, 1}},
	}
	if err := embedstore.Replace(db, state, chunks); err != nil {
		t.Fatalf("Replace against a migrated table: %v", err)
	}

	got, ok, err := embedstore.Get(db, "doc1")
	if err != nil || !ok {
		t.Fatalf("Get: %v (found %v)", err, ok)
	}
	if got.TextHash != "xyz" || got.ChunkCount != 2 {
		t.Fatalf("state = %+v", got)
	}
	rows, err := embedstore.Chunks(db, "doc1")
	if err != nil {
		t.Fatalf("Chunks: %v", err)
	}
	if len(rows) != 2 || len(rows[0].Vector) != 2 {
		t.Fatalf("chunks = %+v", rows)
	}
}
