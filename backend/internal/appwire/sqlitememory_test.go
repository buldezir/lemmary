package appwire

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/pocketbase/dbx"
	"modernc.org/sqlite"

	"lemmary/backend/internal/testpb"
)

func cacheUsed(t *testing.T, conn *sql.Conn) int {
	t.Helper()
	var used int
	err := conn.Raw(func(dc any) error {
		var err error
		used, _, err = dc.(sqlite.DBStatus).Status(sqlite.DBStatusCacheUsed, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return used
}

// The shrink runs on whatever pooled connection is free, so it only helps if
// it also empties the cache of a connection it never touched.
func TestShrinkSQLiteMemoryReachesOtherConnections(t *testing.T) {
	app := testpb.Open(t)
	ctx := context.Background()

	held, err := app.ConcurrentDB().(*dbx.DB).DB().Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	for _, q := range []string{
		"CREATE TABLE shrink_probe (body TEXT)",
		"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 200) INSERT INTO shrink_probe SELECT ? FROM n",
	} {
		if _, err := held.ExecContext(ctx, q, strings.Repeat("x", 8000)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := held.ExecContext(ctx, "SELECT count(body) FROM shrink_probe"); err != nil {
		t.Fatal(err)
	}
	before := cacheUsed(t, held)
	if before < 1<<20 {
		t.Fatalf("cache_used = %d before shrink, want the probe table cached", before)
	}

	shrinkSQLiteMemory(app)

	if after := cacheUsed(t, held); after > before/10 {
		t.Fatalf("cache_used = %d after shrink, was %d", after, before)
	}
}
