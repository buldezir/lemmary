package vault

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pocketbase/dbx"
)

// pbSnapshotter captures the application databases with VACUUM INTO.
//
// VACUUM INTO produces a consistent single-file snapshot from a read
// transaction without blocking writers, which is what lets a flush run every few
// seconds on a live system. It is strictly better here than PocketBase's own
// backup approach of copying data.db plus its WAL inside a write transaction:
// that blocks every writer for the duration, and copying a -wal/-shm pair while
// anything is writing is how you get a torn database.
type pbSnapshotter struct {
	databases map[string]dbx.Builder
}

// NewPocketBaseSnapshotter returns a Snapshotter over the app's two databases.
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
		// VACUUM INTO refuses to overwrite, and a leftover file from an aborted
		// flush would otherwise fail every subsequent one.
		if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
			return err
		}

		// The destination has to be embedded in the SQL text; it is a path this
		// process constructed under its own staging directory, but quote it
		// properly and refuse anything unexpected rather than trusting that.
		if strings.ContainsAny(dst, "'\x00\n") {
			return fmt.Errorf("vault: refusing to snapshot into %q", dst)
		}

		// Checkpointing first keeps the WAL from growing without bound; it is
		// best-effort because a busy database simply defers it to the next flush.
		if _, err := db.NewQuery("PRAGMA wal_checkpoint(TRUNCATE)").Execute(); err != nil {
			// Not fatal: VACUUM INTO still reads a consistent view.
			_ = err
		}
		if _, err := db.NewQuery("VACUUM INTO '" + dst + "'").Execute(); err != nil {
			return fmt.Errorf("vault: vacuum %s: %w", name, err)
		}
	}
	return nil
}
