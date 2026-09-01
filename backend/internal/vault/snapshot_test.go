package vault

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	_ "modernc.org/sqlite"
)

// openTestDB opens a WAL database with the same pragmas PocketBase uses, and
// with more than one connection, which is what the concurrent pool is.
func openTestDB(t *testing.T, path string) *dbx.DB {
	t.Helper()
	pragmas := "?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := dbx.Open("sqlite", path+pragmas)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	db.DB().SetMaxOpenConns(4)
	if _, err := db.NewQuery("CREATE TABLE docs (id INTEGER PRIMARY KEY, body TEXT)").Execute(); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return db
}

// The snapshot runs on the concurrent pool, so it has to work there: VACUUM INTO
// is a read transaction, but it is still being issued on a connection from a
// pool rather than on the single write connection, and a snapshot that silently
// failed would take the databases out of every flush.
func TestSnapshotterWritesReadableDatabases(t *testing.T) {
	dir := t.TempDir()
	data := openTestDB(t, filepath.Join(dir, "source.db"))
	aux := openTestDB(t, filepath.Join(dir, "auxsource.db"))

	if _, err := data.NewQuery("INSERT INTO docs (body) VALUES ('a document')").Execute(); err != nil {
		t.Fatalf("insert: %v", err)
	}

	stage := t.TempDir()
	if err := NewPocketBaseSnapshotter(data, aux).SnapshotDatabases(stage); err != nil {
		t.Fatalf("SnapshotDatabases: %v", err)
	}

	snap := openSnapshot(t, filepath.Join(stage, "data.db"))
	var body string
	if err := snap.NewQuery("SELECT body FROM docs LIMIT 1").Row(&body); err != nil {
		t.Fatalf("read the snapshot: %v", err)
	}
	if body != "a document" {
		t.Fatalf("snapshot content = %q", body)
	}
	if _, err := openSnapshot(t, filepath.Join(stage, "auxiliary.db")).NewQuery("SELECT 1").Execute(); err != nil {
		t.Fatalf("the auxiliary snapshot is unreadable: %v", err)
	}
}

// A leftover file from an aborted flush must not fail every later one: VACUUM
// INTO refuses to overwrite.
func TestSnapshotterOverwritesAStaleStagedFile(t *testing.T) {
	dir := t.TempDir()
	data := openTestDB(t, filepath.Join(dir, "source.db"))
	aux := openTestDB(t, filepath.Join(dir, "auxsource.db"))
	s := NewPocketBaseSnapshotter(data, aux)

	stage := t.TempDir()
	if err := s.SnapshotDatabases(stage); err != nil {
		t.Fatalf("first snapshot: %v", err)
	}
	if err := s.SnapshotDatabases(stage); err != nil {
		t.Fatalf("second snapshot over a stale file: %v", err)
	}
}

// The reason for choosing VACUUM INTO over copying the database and its WAL was
// that it does not block writers. Issuing it on the single write connection
// gives that property straight back — every write on the instance would stall
// for the length of a full database read, on every flush — so the snapshot runs
// on the concurrent pool and writers must keep going while it does.
func TestSnapshotDoesNotBlockWriters(t *testing.T) {
	dir := t.TempDir()
	data := openTestDB(t, filepath.Join(dir, "source.db"))
	aux := openTestDB(t, filepath.Join(dir, "auxsource.db"))

	// Enough rows that the snapshot is not instantaneous.
	for i := 0; i < 2000; i++ {
		if _, err := data.NewQuery("INSERT INTO docs (body) VALUES ('padding padding padding padding')").Execute(); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	writes := 0
	stop := make(chan struct{})
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := data.NewQuery("INSERT INTO docs (body) VALUES ('written during the snapshot')").Execute(); err != nil {
				t.Errorf("a write failed during the snapshot: %v", err)
				return
			}
			writes++
			time.Sleep(time.Millisecond)
		}
	}()

	if err := NewPocketBaseSnapshotter(data, aux).SnapshotDatabases(t.TempDir()); err != nil {
		t.Fatalf("SnapshotDatabases: %v", err)
	}
	close(stop)
	wg.Wait()

	if writes == 0 {
		t.Fatal("no write completed while the snapshot ran")
	}
}

func openSnapshot(t *testing.T, path string) *dbx.DB {
	t.Helper()
	db, err := dbx.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open snapshot %s: %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
