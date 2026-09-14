// Package embedstore keeps document chunk vectors in raw SQLite tables inside
// the PocketBase data database.
//
// Raw tables rather than a collection: a 3000-chunk document would be 3000
// records each paying record hooks, validation and an events fan-out, and the
// backfill can ask for its candidates as one SQL join. The tables still live in
// data.db, so the vault snapshot and PocketBase's backup cover them, and inside
// an encrypted instance they are ciphertext at rest.
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

type State struct {
	DocumentID     string
	UserID         string
	Model          string
	Dims           int
	ChunkerVersion int
	TextHash       string
	ChunkCount     int
	Truncated      bool
	Status         string
	Stale          bool
	Attempts       int
	NextAttemptAt  string
	LastError      string
	EmbeddedAt     string
}

// Chunk is a slice of documents.ocr_text, stored as byte offsets rather than a
// copy: duplicating the column would double the archive for no retrieval gain.
type Chunk struct {
	DocumentID string
	Ordinal    int
	UserID     string
	StartByte  int
	EndByte    int
	Model      string
	Dims       int
	Vector     []float32
}

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

// Safe to call on every boot.
func EnsureSchema(db dbx.Builder) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS ` + tableEmbeddings + ` (
			document_id     TEXT PRIMARY KEY,
			user            TEXT NOT NULL DEFAULT '',
			model           TEXT NOT NULL DEFAULT '',
			dims            INTEGER NOT NULL DEFAULT 0,
			chunker_version INTEGER NOT NULL DEFAULT 0,
			text_hash       TEXT NOT NULL DEFAULT '',
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
			start_byte  INTEGER NOT NULL DEFAULT 0,
			end_byte    INTEGER NOT NULL DEFAULT 0,
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

func DropSchema(db dbx.Builder) error {
	for _, table := range []string{tableChunks, tableEmbeddings} {
		if _, err := db.NewQuery(`DROP TABLE IF EXISTS ` + table).Execute(); err != nil {
			return fmt.Errorf("embedstore drop %s: %w", table, err)
		}
	}
	return nil
}

// TextHash identifies the exact text a set of chunks was cut from, so a re-run
// over unchanged text skips the document without one provider request.
func TextHash(values ...string) string {
	h := sha256.New()
	for _, v := range values {
		// Without the separator "ab"+"c" and "a"+"bc" would hash the same.
		h.Write([]byte(v))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Little-endian float32, exactly what Bleve's vector_base64 field decodes, so
// a stored BLOB reaches the index without a conversion pass.
func EncodeVector(vec []float32) []byte {
	out := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

// A blob whose length is not a multiple of four is corrupt, not short, so it
// decodes to nothing.
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
