package embedstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/dbx"

	"lemmary/backend/internal/strutil"
)

// maxLastError keeps one provider's HTML error page from becoming most of a row.
const maxLastError = 1000

// forEachChunkPage is the keyset page size for a full scan. Small enough that
// a rebuild of a large archive never holds the whole corpus in memory.
const forEachChunkPage = 500

type stateRow struct {
	DocumentID     string `db:"document_id"`
	UserID         string `db:"user"`
	Model          string `db:"model"`
	Dims           int    `db:"dims"`
	ChunkerVersion int    `db:"chunker_version"`
	TextHash       string `db:"text_hash"`
	HeaderHash     string `db:"header_hash"`
	ChunkCount     int    `db:"chunk_count"`
	Truncated      int    `db:"truncated"`
	Status         string `db:"status"`
	Stale          int    `db:"stale"`
	Attempts       int    `db:"attempts"`
	NextAttemptAt  string `db:"next_attempt_at"`
	LastError      string `db:"last_error"`
	EmbeddedAt     string `db:"embedded_at"`
}

func (r stateRow) toState() State {
	return State{
		DocumentID:     r.DocumentID,
		UserID:         r.UserID,
		Model:          r.Model,
		Dims:           r.Dims,
		ChunkerVersion: r.ChunkerVersion,
		TextHash:       r.TextHash,
		HeaderHash:     r.HeaderHash,
		ChunkCount:     r.ChunkCount,
		Truncated:      r.Truncated != 0,
		Status:         r.Status,
		Stale:          r.Stale != 0,
		Attempts:       r.Attempts,
		NextAttemptAt:  r.NextAttemptAt,
		LastError:      r.LastError,
		EmbeddedAt:     r.EmbeddedAt,
	}
}

type chunkRow struct {
	DocumentID string `db:"document_id"`
	Ordinal    int    `db:"ordinal"`
	UserID     string `db:"user"`
	Kind       string `db:"kind"`
	StartByte  int    `db:"start_byte"`
	EndByte    int    `db:"end_byte"`
	Text       string `db:"text"`
	Model      string `db:"model"`
	Dims       int    `db:"dims"`
	Vector     []byte `db:"vector"`
}

func (r chunkRow) toChunk() Chunk {
	return Chunk{
		DocumentID: r.DocumentID,
		Ordinal:    r.Ordinal,
		UserID:     r.UserID,
		Kind:       r.Kind,
		StartByte:  r.StartByte,
		EndByte:    r.EndByte,
		Text:       r.Text,
		Model:      r.Model,
		Dims:       r.Dims,
		Vector:     DecodeVector(r.Vector),
	}
}

// Replace writes a document's chunks and state as one set, dropping whatever was
// there before.
//
// Replace rather than merge, because a re-embed after an edit produces a
// different number of chunks at different offsets: merging would leave the tail
// of the previous run behind as passages that no longer exist in the text. The
// caller runs this inside a transaction so a half-replaced document is never
// visible.
func Replace(db dbx.Builder, state State, chunks []Chunk) error {
	if strings.TrimSpace(state.DocumentID) == "" {
		return errors.New("embedstore: replace needs a document id")
	}
	if _, err := db.Delete(tableChunks, dbx.HashExp{"document_id": state.DocumentID}).Execute(); err != nil {
		return fmt.Errorf("embedstore: clear chunks: %w", err)
	}

	for _, c := range chunks {
		if len(c.Vector) == 0 {
			return fmt.Errorf("embedstore: chunk %d of %s has no vector", c.Ordinal, state.DocumentID)
		}
		if state.Dims > 0 && len(c.Vector) != state.Dims {
			// A wrong-length vector is dropped silently by the vector index, so
			// letting one reach the table would produce a document that looks
			// embedded and can never be found.
			return fmt.Errorf("embedstore: chunk %d of %s is %d dimensions, state says %d",
				c.Ordinal, state.DocumentID, len(c.Vector), state.Dims)
		}
		_, err := db.Insert(tableChunks, dbx.Params{
			"document_id": state.DocumentID,
			"ordinal":     c.Ordinal,
			"user":        state.UserID,
			"kind":        c.Kind,
			"start_byte":  c.StartByte,
			"end_byte":    c.EndByte,
			"text":        c.Text,
			"model":       state.Model,
			"dims":        len(c.Vector),
			"vector":      EncodeVector(c.Vector),
		}).Execute()
		if err != nil {
			return fmt.Errorf("embedstore: insert chunk %d: %w", c.Ordinal, err)
		}
	}

	state.Status = normalizeStatus(state.Status)
	state.ChunkCount = len(chunks)
	if state.EmbeddedAt == "" {
		state.EmbeddedAt = nowTimestamp()
	}
	return upsertState(db, state)
}

func upsertState(db dbx.Builder, state State) error {
	params := dbx.Params{
		"document_id":     state.DocumentID,
		"user":            state.UserID,
		"model":           state.Model,
		"dims":            state.Dims,
		"chunker_version": state.ChunkerVersion,
		"text_hash":       state.TextHash,
		"header_hash":     state.HeaderHash,
		"chunk_count":     state.ChunkCount,
		"truncated":       boolToInt(state.Truncated),
		"status":          state.Status,
		"stale":           boolToInt(state.Stale),
		"attempts":        state.Attempts,
		"next_attempt_at": state.NextAttemptAt,
		"last_error":      strutil.Truncate(state.LastError, maxLastError),
		"embedded_at":     state.EmbeddedAt,
	}
	columns := make([]string, 0, len(params))
	placeholders := make([]string, 0, len(params))
	assignments := make([]string, 0, len(params))
	for _, name := range stateColumns {
		columns = append(columns, name)
		placeholders = append(placeholders, "{:"+name+"}")
		if name != "document_id" {
			assignments = append(assignments, name+" = excluded."+name)
		}
	}

	query := "INSERT INTO " + tableEmbeddings + " (" + strings.Join(columns, ", ") + ")" +
		" VALUES (" + strings.Join(placeholders, ", ") + ")" +
		" ON CONFLICT(document_id) DO UPDATE SET " + strings.Join(assignments, ", ")
	if _, err := db.NewQuery(query).Bind(params).Execute(); err != nil {
		return fmt.Errorf("embedstore: save state: %w", err)
	}
	return nil
}

// stateColumns fixes the column order so the insert and the upsert assignment
// list cannot drift apart.
var stateColumns = []string{
	"document_id", "user", "model", "dims", "chunker_version", "text_hash",
	"header_hash", "chunk_count", "truncated", "status", "stale", "attempts",
	"next_attempt_at", "last_error", "embedded_at",
}

// MarkFailed records a failed attempt and when to try again. The attempt
// counter is incremented in SQL so two concurrent failures cannot both write
// back the same count they read.
//
// The chunks a previous successful run left behind are kept on purpose: a
// provider outage should degrade retrieval to what it was, not delete a
// document out of the dense index.
func MarkFailed(db dbx.Builder, documentID, userID string, cause error, nextAttempt time.Time) error {
	message := ""
	if cause != nil {
		message = strutil.Truncate(cause.Error(), maxLastError)
	}
	next := ""
	if !nextAttempt.IsZero() {
		next = nextAttempt.UTC().Format(timestampLayout)
	}

	query := `INSERT INTO ` + tableEmbeddings + `
			(document_id, user, status, attempts, next_attempt_at, last_error)
		VALUES ({:id}, {:user}, {:status}, 1, {:next}, {:err})
		ON CONFLICT(document_id) DO UPDATE SET
			status = {:status},
			attempts = ` + tableEmbeddings + `.attempts + 1,
			next_attempt_at = {:next},
			last_error = {:err}`
	params := dbx.Params{
		"id": documentID, "user": userID, "status": StatusFailed,
		"next": next, "err": message,
	}
	if _, err := db.NewQuery(query).Bind(params).Execute(); err != nil {
		return fmt.Errorf("embedstore: mark failed: %w", err)
	}
	return nil
}

// MarkStale flags a document whose text or metadata changed. The chunks stay
// readable until the backfill replaces them: a slightly out-of-date passage is
// a better answer than no passage.
func MarkStale(db dbx.Builder, documentID string) error {
	_, err := db.NewQuery(`UPDATE ` + tableEmbeddings + ` SET stale = 1 WHERE document_id = {:id}`).
		Bind(dbx.Params{"id": documentID}).Execute()
	if err != nil {
		return fmt.Errorf("embedstore: mark stale: %w", err)
	}
	return nil
}

// Delete removes a document's chunks and state.
func Delete(db dbx.Builder, documentID string) error {
	if _, err := db.Delete(tableChunks, dbx.HashExp{"document_id": documentID}).Execute(); err != nil {
		return fmt.Errorf("embedstore: delete chunks: %w", err)
	}
	if _, err := db.Delete(tableEmbeddings, dbx.HashExp{"document_id": documentID}).Execute(); err != nil {
		return fmt.Errorf("embedstore: delete state: %w", err)
	}
	return nil
}

// Get returns a document's state. The bool is false when there is no row, which
// is not an error: it is what an unembedded document looks like.
func Get(db dbx.Builder, documentID string) (State, bool, error) {
	var row stateRow
	err := db.Select().From(tableEmbeddings).
		Where(dbx.HashExp{"document_id": documentID}).One(&row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return State{}, false, nil
		}
		return State{}, false, fmt.Errorf("embedstore: get state: %w", err)
	}
	return row.toState(), true, nil
}

// Chunks returns one document's chunks in ordinal order.
func Chunks(db dbx.Builder, documentID string) ([]Chunk, error) {
	var rows []chunkRow
	err := db.Select().From(tableChunks).
		Where(dbx.HashExp{"document_id": documentID}).
		OrderBy("ordinal ASC").All(&rows)
	if err != nil {
		return nil, fmt.Errorf("embedstore: list chunks: %w", err)
	}
	out := make([]Chunk, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toChunk())
	}
	return out, nil
}

// ForEachChunk walks every chunk written by model at dims, in a stable order,
// one page at a time.
//
// The model/dims filter is not an optimisation: a vector of the wrong length is
// dropped silently at index time, so an index rebuilt after a model switch
// would come back quietly incomplete without it.
func ForEachChunk(db dbx.Builder, model string, dims int, fn func(Chunk) error) error {
	lastDoc := ""
	lastOrd := -1
	for {
		var rows []chunkRow
		err := db.Select().From(tableChunks).
			Where(dbx.NewExp(
				"model = {:model} AND dims = {:dims} AND (document_id > {:doc} OR (document_id = {:doc} AND ordinal > {:ord}))",
				dbx.Params{"model": model, "dims": dims, "doc": lastDoc, "ord": lastOrd},
			)).
			OrderBy("document_id ASC", "ordinal ASC").
			Limit(forEachChunkPage).All(&rows)
		if err != nil {
			return fmt.Errorf("embedstore: scan chunks: %w", err)
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if err := fn(row.toChunk()); err != nil {
				return err
			}
		}
		lastDoc = rows[len(rows)-1].DocumentID
		lastOrd = rows[len(rows)-1].Ordinal
	}
}

// CountChunks is how many chunks were written by model at dims.
//
// It is what the vector index compares itself against to decide whether it has
// drifted, so it counts exactly what ForEachChunk would walk: same filter, same
// reason for it.
func CountChunks(db dbx.Builder, model string, dims int) (int, error) {
	if strings.TrimSpace(model) == "" || dims <= 0 {
		return 0, nil
	}
	var n int
	err := db.NewQuery(`SELECT COUNT(*) FROM ` + tableChunks + `
		WHERE model = {:model} AND dims = {:dims}`).
		Bind(dbx.Params{"model": model, "dims": dims}).Row(&n)
	if err != nil {
		return 0, fmt.Errorf("embedstore: count chunks: %w", err)
	}
	return n, nil
}

// candidateWhere is the definition of "this document needs embedding", shared
// by Candidates and Stats so the queue length and the queue cannot disagree.
//
// Pending and processing documents are excluded because their OCR text is about
// to be rewritten; duplicates because they are never shown as results; empty
// text because there is nothing to embed.
//
// A failed row is governed by its backoff alone. Folding the freshness tests
// into it would make a document that failed against a model the admin has since
// changed retry immediately and keep failing, defeating the backoff at the one
// moment it matters.
const candidateWhere = `
	d.duplicate_of = '' AND d.ocr_text <> ''
	AND d.processing_status NOT IN ('pending', 'processing')
	AND (
		e.document_id IS NULL
		OR (e.status = {:failed} AND (e.next_attempt_at = '' OR e.next_attempt_at <= {:now}))
		OR (e.status <> {:failed} AND (
			e.stale = 1
			OR e.model <> {:model}
			OR ({:dims} > 0 AND e.dims <> {:dims})
			OR e.chunker_version <> {:version}
		))
	)`

// Candidates lists the documents the backfill should embed next, oldest first.
func Candidates(db dbx.Builder, model string, dims, chunkerVersion, limit int, now time.Time) ([]string, error) {
	if strings.TrimSpace(model) == "" || limit <= 0 {
		return nil, nil
	}
	query := `SELECT d.id FROM documents d
		LEFT JOIN ` + tableEmbeddings + ` e ON e.document_id = d.id
		WHERE ` + candidateWhere + `
		ORDER BY d.created ASC
		LIMIT {:limit}`

	var ids []string
	err := db.NewQuery(query).Bind(candidateParams(model, dims, chunkerVersion, now, limit)).Column(&ids)
	if err != nil {
		return nil, fmt.Errorf("embedstore: candidates: %w", err)
	}
	return ids, nil
}

func candidateParams(model string, dims, chunkerVersion int, now time.Time, limit int) dbx.Params {
	return dbx.Params{
		"model":   model,
		"dims":    dims,
		"version": chunkerVersion,
		"failed":  StatusFailed,
		"now":     now.UTC().Format(timestampLayout),
		"limit":   limit,
	}
}

// DeleteOrphans removes rows whose document is gone.
//
// The record hook already deletes on the way out, so this is the repair path
// for everything that bypasses it: a document deleted while the feature was
// off, a restored backup, a cascade the hook missed because the process died
// between the two writes.
func DeleteOrphans(db dbx.Builder) (int, error) {
	total := 0
	for _, table := range []string{tableChunks, tableEmbeddings} {
		res, err := db.NewQuery(`DELETE FROM ` + table + `
			WHERE document_id NOT IN (SELECT id FROM documents)`).Execute()
		if err != nil {
			return total, fmt.Errorf("embedstore: delete orphans from %s: %w", table, err)
		}
		if n, err := res.RowsAffected(); err == nil && table == tableEmbeddings {
			total = int(n)
		}
	}
	return total, nil
}

// LoadStats counts the backlog for the Settings page. Total is the documents that
// are embeddable at all, so "embedded of total" reads as a real progress bar
// rather than counting drafts and duplicates nobody will ever search.
func LoadStats(db dbx.Builder, model string, dims, chunkerVersion int, now time.Time) (Stats, error) {
	out := Stats{Model: model, Dims: dims, Enabled: strings.TrimSpace(model) != ""}
	if !out.Enabled {
		return out, nil
	}

	counts := []struct {
		into  *int
		query string
		bind  dbx.Params
	}{
		{&out.Total, `SELECT COUNT(*) FROM documents d
			WHERE d.duplicate_of = '' AND d.ocr_text <> ''
			AND d.processing_status NOT IN ('pending', 'processing')`, nil},
		{&out.Embedded, `SELECT COUNT(*) FROM ` + tableEmbeddings + `
			WHERE status = {:ok} AND stale = 0 AND model = {:model}
			AND ({:dims} = 0 OR dims = {:dims}) AND chunker_version = {:version}`,
			dbx.Params{"ok": StatusOK, "model": model, "dims": dims, "version": chunkerVersion}},
		{&out.Stale, `SELECT COUNT(*) FROM ` + tableEmbeddings + ` WHERE stale = 1`, nil},
		{&out.Failed, `SELECT COUNT(*) FROM ` + tableEmbeddings + ` WHERE status = {:failed}`,
			dbx.Params{"failed": StatusFailed}},
		{&out.Chunks, `SELECT COUNT(*) FROM ` + tableChunks + `
			WHERE model = {:model} AND ({:dims} = 0 OR dims = {:dims})`,
			dbx.Params{"model": model, "dims": dims}},
		{&out.Pending, `SELECT COUNT(*) FROM documents d
			LEFT JOIN ` + tableEmbeddings + ` e ON e.document_id = d.id
			WHERE ` + candidateWhere,
			candidateParams(model, dims, chunkerVersion, now, 1)},
	}

	for _, c := range counts {
		q := db.NewQuery(c.query)
		if c.bind != nil {
			q = q.Bind(c.bind)
		}
		if err := q.Row(c.into); err != nil {
			return out, fmt.Errorf("embedstore: stats: %w", err)
		}
	}
	return out, nil
}

const timestampLayout = "2006-01-02 15:04:05.000Z"

func nowTimestamp() string { return time.Now().UTC().Format(timestampLayout) }
