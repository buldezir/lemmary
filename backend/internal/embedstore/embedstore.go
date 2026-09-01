// Package embedstore keeps document chunk vectors in raw SQLite tables inside
// the PocketBase data database.
//
// Why not a PocketBase collection. A 1536-dimension vector is 6 KB as a float32
// BLOB and about 8 KB as base64 inside a JSON field; a 3000-chunk document is
// 3000 records, each paying record hooks, field validation and an events fan-out
// on every write. Raw tables also keep the vectors off /api/collections
// entirely, and let the backfill ask for its candidates as one SQL join against
// `documents` rather than as thousands of round trips.
//
// Nothing is lost by leaving PocketBase's record layer: the tables live in
// data.db, which the vault snapshots whole and PocketBase's own backup zips, so
// the vectors are covered by both without any work here. Inside an encrypted
// instance that also means they are ciphertext at rest, which a Bleve index in
// tmpfs could never promise on its own.
//
// There are no SQL foreign keys, because PocketBase does not enable
// foreign_keys on its connections; the cascade is a record hook plus an orphan
// sweep on every backfill tick, which also repairs deletions that happened
// while the feature was off.
package embedstore

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/pocketbase/dbx"
)

const (
	tableEmbeddings = "document_embeddings"
	tableChunks     = "document_chunks"
)

// Status values for a document's embedding row.
const (
	StatusOK     = "ok"
	StatusFailed = "failed"
)

// Chunk kinds. The header chunk is ordinal 0 and carries rendered metadata
// rather than a slice of the OCR text, which is why it stores its own text.
const (
	KindHeader = "header"
	KindBody   = "body"
)

// State is one document's embedding bookkeeping: what was embedded, with what,
// and whether it is still current.
type State struct {
	DocumentID     string
	UserID         string
	Model          string
	Dims           int
	ChunkerVersion int
	TextHash       string
	HeaderHash     string
	ChunkCount     int
	Truncated      bool
	Status         string
	Stale          bool
	Attempts       int
	NextAttemptAt  string
	LastError      string
	EmbeddedAt     string
}

// Chunk is one embedded passage. Body chunks store byte offsets into
// documents.ocr_text rather than a copy of the text: the column is the single
// source of truth, and duplicating it would double the archive's size for no
// retrieval benefit.
type Chunk struct {
	DocumentID string
	Ordinal    int
	UserID     string
	Kind       string
	StartByte  int
	EndByte    int
	Text       string
	Model      string
	Dims       int
	Vector     []float32
}

// Stats is what the Settings page shows about the embedding backlog.
type Stats struct {
	Enabled  bool   `json:"enabled"`
	Model    string `json:"model"`
	Dims     int    `json:"dims"`
	Total    int    `json:"total"`
	Embedded int    `json:"embedded"`
	Stale    int    `json:"stale"`
	Failed   int    `json:"failed"`
	Pending  int    `json:"pending"`
	Chunks   int    `json:"chunks"`
}

// EnsureSchema creates the tables. Safe to call on every boot; the migration
// calls it once and the tests call it directly.
func EnsureSchema(db dbx.Builder) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS ` + tableEmbeddings + ` (
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
		`CREATE INDEX IF NOT EXISTS idx_document_embeddings_state
			ON ` + tableEmbeddings + ` (status, stale, next_attempt_at)`,
		`CREATE TABLE IF NOT EXISTS ` + tableChunks + ` (
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
		`CREATE INDEX IF NOT EXISTS idx_document_chunks_user ON ` + tableChunks + ` (user)`,
	}
	for _, sql := range statements {
		if _, err := db.NewQuery(sql).Execute(); err != nil {
			return fmt.Errorf("embedstore schema: %w", err)
		}
	}
	return nil
}

// DropSchema removes both tables. Used by the migration's down step.
func DropSchema(db dbx.Builder) error {
	for _, table := range []string{tableChunks, tableEmbeddings} {
		if _, err := db.NewQuery(`DROP TABLE IF EXISTS ` + table).Execute(); err != nil {
			return fmt.Errorf("embedstore drop %s: %w", table, err)
		}
	}
	return nil
}

// TextHash identifies the exact text a set of chunks was cut from. Comparing it
// is what tells a re-run that nothing changed, and so that the whole document
// can be skipped without a single request to the provider.
func TextHash(values ...string) string {
	h := sha256.New()
	for _, v := range values {
		// The separator matters: without it "ab"+"c" and "a"+"bc" would hash
		// the same, and a metadata edit that only moved a word between fields
		// would look like no change at all.
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// EncodeVector packs a vector as little-endian float32, which is exactly the
// layout Bleve's vector_base64 field decodes, so a stored BLOB can be handed to
// the index without a conversion pass.
func EncodeVector(vec []float32) []byte {
	out := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

// DecodeVector reverses EncodeVector. A blob whose length is not a multiple of
// four is corrupt rather than short, so it decodes to nothing.
func DecodeVector(raw []byte) []float32 {
	if len(raw) == 0 || len(raw)%4 != 0 {
		return nil
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[4*i:]))
	}
	return out
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func normalizeStatus(s string) string {
	if strings.TrimSpace(s) == StatusFailed {
		return StatusFailed
	}
	return StatusOK
}
