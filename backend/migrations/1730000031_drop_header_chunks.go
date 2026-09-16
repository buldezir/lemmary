package migrations

import (
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Embeddings are built from documents.ocr_text and nothing else.
//
// Until now every document also carried a "header" chunk at ordinal 0: its
// title, tags, correspondent and summary embedded beside the text. That passage
// is what made a rename expensive, dating vectors the OCR text had not changed a
// byte of, and the metadata it carried is already searchable through the keyword
// index.
//
// The header rows are deleted rather than re-embedded away: a body chunk's
// vector is a function of its slice of ocr_text, which does not change here, so
// bumping chunk.Version would re-buy an entire archive's embeddings to produce
// identical numbers.
//
// document_chunks is rebuilt in one pass rather than edited in three. SQLite
// implements DROP COLUMN by copying the whole table, and this is the table that
// holds every float32 blob in the archive, so dropping two columns separately
// would copy all of it twice. That matters most under VAULT_ENABLED=1, where
// data.db lives in a tmpfs and the copy is RAM.
//
// App migrations run before the Bleve chunk index opens, so ShouldHeal sees the
// chunk count drop and rebuilds synchronously before the process serves. No
// header passage is ever quoted after this runs, at the cost of one slow first
// boot on a large archive.
func init() {
	m.Register(func(app core.App) error {
		return dropHeaderChunks(app.DB())
	}, func(app core.App) error {
		return restoreHeaderChunkColumns(app.DB())
	})
}

// newChunksTable is the shape document_chunks has after this migration.
//
// Spelled out here rather than taken from embedstore.EnsureSchema on purpose: a
// migration has to keep producing the shape of *its own* moment. If a later
// release adds a column, EnsureSchema gains it and this must not, or replaying
// history on a fresh database stops matching what the upgrade path produces.
const newChunksTable = `CREATE TABLE document_chunks_new (
	document_id TEXT NOT NULL,
	ordinal     INTEGER NOT NULL,
	user        TEXT NOT NULL DEFAULT '',
	start_byte  INTEGER NOT NULL DEFAULT 0,
	end_byte    INTEGER NOT NULL DEFAULT 0,
	model       TEXT NOT NULL DEFAULT '',
	dims        INTEGER NOT NULL DEFAULT 0,
	vector      BLOB NOT NULL,
	PRIMARY KEY (document_id, ordinal)
) WITHOUT ROWID`

// dropHeaderChunks purges the header rows and the columns only they used.
func dropHeaderChunks(db dbx.Builder) error {
	has, err := hasColumn(db, "document_chunks", "kind")
	if err != nil {
		return err
	}
	if has {
		// Ordered, and each statement depends on the last: build the new shape,
		// copy the body rows into it, then swap. Dropping the old table takes
		// idx_document_chunks_user with it, so the index is recreated after the
		// rename.
		statements := []string{
			newChunksTable,
			`INSERT INTO document_chunks_new
				SELECT document_id, ordinal, user, start_byte, end_byte, model, dims, vector
				FROM document_chunks WHERE kind <> 'header'`,
			`DROP TABLE document_chunks`,
			`ALTER TABLE document_chunks_new RENAME TO document_chunks`,
			`CREATE INDEX IF NOT EXISTS idx_document_chunks_user ON document_chunks (user)`,
		}
		for _, sql := range statements {
			if _, err := db.NewQuery(sql).Execute(); err != nil {
				return fmt.Errorf("rebuild document_chunks: %w", err)
			}
		}
	}

	// chunk_count is read once, on the skipped-document path, and only to log
	// it. Correcting it costs one statement and keeps the number honest until
	// each document is next re-embedded. Run after the rebuild, so it counts
	// what actually survived.
	_, err = db.NewQuery(`UPDATE document_embeddings SET chunk_count = (
		SELECT COUNT(*) FROM document_chunks c WHERE c.document_id = document_embeddings.document_id
	)`).Execute()
	if err != nil {
		return fmt.Errorf("correct chunk counts: %w", err)
	}

	// One column on a table of one short row per document, so the rewrite
	// DROP COLUMN performs is cheap here in a way it is not on the chunks.
	has, err = hasColumn(db, "document_embeddings", "header_hash")
	if err != nil {
		return err
	}
	if has {
		if _, err := db.NewQuery(`ALTER TABLE document_embeddings DROP COLUMN header_hash`).Execute(); err != nil {
			return fmt.Errorf("drop header_hash: %w", err)
		}
	}
	return nil
}

// restoreHeaderChunkColumns puts the columns back, empty.
//
// The header rows themselves are not recoverable, and do not need to be: an
// older binary reading header_hash = ” against a document whose header renders
// to anything at all sees a stale document and re-embeds it, header included.
func restoreHeaderChunkColumns(db dbx.Builder) error {
	adds := []struct{ table, column, ddl string }{
		{"document_chunks", "kind", `ALTER TABLE document_chunks ADD COLUMN kind TEXT NOT NULL DEFAULT 'body'`},
		{"document_chunks", "text", `ALTER TABLE document_chunks ADD COLUMN text TEXT NOT NULL DEFAULT ''`},
		{"document_embeddings", "header_hash", `ALTER TABLE document_embeddings ADD COLUMN header_hash TEXT NOT NULL DEFAULT ''`},
	}
	for _, add := range adds {
		has, err := hasColumn(db, add.table, add.column)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err := db.NewQuery(add.ddl).Execute(); err != nil {
			return fmt.Errorf("restore %s.%s: %w", add.table, add.column, err)
		}
	}
	return nil
}

// hasColumn asks the schema rather than running the statement and reading the
// error text. A fresh install reaches this migration with the new shape already
// created by EnsureSchema, so "the column is not there" is an ordinary answer
// here, not a failure to pattern-match against an error string.
func hasColumn(db dbx.Builder, table, column string) (bool, error) {
	var names []string
	err := db.NewQuery(`SELECT name FROM pragma_table_info({:table})`).
		Bind(dbx.Params{"table": table}).Column(&names)
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", table, err)
	}
	for _, name := range names {
		if name == column {
			return true, nil
		}
	}
	return false, nil
}
