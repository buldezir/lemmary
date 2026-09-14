package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketbase/dbx"
)

// pbSnapshotter uses VACUUM INTO, which produces a consistent single-file
// snapshot from a read transaction without blocking writers, so a flush can run
// every few seconds on a live system. PocketBase's own backup copies data.db
// plus its WAL inside a write transaction, which blocks every writer and can
// tear the database.
type pbSnapshotter struct {
	databases map[string]dbx.Builder
}

func NewPocketBaseSnapshotter(data, aux dbx.Builder) Snapshotter {
	return &pbSnapshotter{databases: map[string]dbx.Builder{
		"data.db":      data,
		"auxiliary.db": aux,
	}}
}

func (p *pbSnapshotter) SnapshotDatabases(stageDir string) error {
	for name, db := range p.databases {
		if db == nil {
			continue
		}
		dst := filepath.Join(stageDir, name)
		// VACUUM INTO refuses to overwrite, and a leftover file from an aborted flush
		// would fail every subsequent one.
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}

		// The destination has to be embedded in the SQL text, so refuse anything
		// unexpected rather than trusting that this process built the path.
		if strings.ContainsAny(dst, "'\x00\n") {
			return fmt.Errorf("vault: refusing to snapshot into %q", dst)
		}

		// Checkpointing keeps the WAL bounded; best-effort because a busy database
		// defers it to the next flush and VACUUM INTO still reads a consistent view.
		if _, err := db.NewQuery("PRAGMA wal_checkpoint(TRUNCATE)").Execute(); err != nil {
			_ = err
		}
		if _, err := db.NewQuery("VACUUM INTO '" + dst + "'").Execute(); err != nil {
			return fmt.Errorf("vault: vacuum %s: %w", name, err)
		}
	}
	return nil
}
