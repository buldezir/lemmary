package ngxapi

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
)

// countDocumentQueries records every SELECT against the documents table so a
// test can assert how many the lookup actually costs.
func countDocumentQueries(t *testing.T, app *pocketbase.PocketBase) func() int {
	t.Helper()
	db, ok := app.ConcurrentDB().(*dbx.DB)
	if !ok {
		t.Skip("ConcurrentDB is not a *dbx.DB on this PocketBase build")
	}

	var (
		mu   sync.Mutex
		seen []string
	)
	previous := db.QueryLogFunc
	db.QueryLogFunc = func(ctx context.Context, d time.Duration, query string, rows *sql.Rows, err error) {
		mu.Lock()
		seen = append(seen, query)
		mu.Unlock()
		if previous != nil {
			previous(ctx, d, query, rows, err)
		}
	}
	t.Cleanup(func() { db.QueryLogFunc = previous })

	return func() int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, query := range seen {
			if strings.Contains(query, "`documents`") {
				n++
			}
		}
		seen = nil
		return n
	}
}

// TestThumbnailGridScansOnce is the traffic that exposed this: a page of
// thumbnails is one request per tile, each for a *different* hashed id, and
// each one used to scan the whole archive to invert its hash. Caching a single
// resolution at a time did nothing here, because no two tiles ask for the same
// id.
func TestThumbnailGridScansOnce(t *testing.T) {
	f := newListFixture(t)
	resetNgxIDCache(t)

	queries := countDocumentQueries(t, f.app)
	for _, pbID := range []string{f.docBoth, f.docOne, f.docNone} {
		record, err := findRecordByNgxID(f.app, "documents", toNgxID(pbID), f.userID)
		if err != nil {
			t.Fatalf("lookup %s: %v", pbID, err)
		}
		if record.Id != pbID {
			t.Fatalf("resolved to %s, want %s", record.Id, pbID)
		}
	}

	// One scan for the first tile, then a primary-key fetch per tile.
	if n, want := queries(), 1+3; n != want {
		t.Fatalf("three distinct lookups ran %d document queries, want %d", n, want)
	}
}

// TestFindRecordByNgxIDVerifiesTheCachedHint: the cache is a hint, never an
// answer. A record deleted after it was resolved must stop resolving, or a
// deleted document would keep serving its file.
func TestFindRecordByNgxIDVerifiesTheCachedHint(t *testing.T) {
	f := newListFixture(t)
	ngxID := toNgxID(f.docOne)

	if _, err := findRecordByNgxID(f.app, "documents", ngxID, f.userID); err != nil {
		t.Fatalf("first lookup: %v", err)
	}

	record, err := f.app.FindRecordById("documents", f.docOne)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	if err := f.app.Delete(record); err != nil {
		t.Fatalf("delete document: %v", err)
	}

	if _, err := findRecordByNgxID(f.app, "documents", ngxID, f.userID); err == nil {
		t.Fatal("a deleted document still resolved from the cache")
	}
}

// TestFindRecordByNgxIDStaysWithinTheOwner: the cache is keyed per owner, and
// the ownership check behind it is what makes a shared entry harmless.
func TestFindRecordByNgxIDStaysWithinTheOwner(t *testing.T) {
	f := newListFixture(t)
	ngxID := toNgxID(f.docTheir)

	if _, err := findRecordByNgxID(f.app, "documents", ngxID, f.otherID); err != nil {
		t.Fatalf("owner lookup: %v", err)
	}
	if _, err := findRecordByNgxID(f.app, "documents", ngxID, f.userID); err == nil {
		t.Fatal("another owner's document resolved")
	}
}

func TestNgxIDMapCoversTheOwnerScope(t *testing.T) {
	f := newListFixture(t)

	known, err := ngxIDMap(f.app, "documents", f.userID)
	if err != nil {
		t.Fatalf("build map: %v", err)
	}
	if len(known) != 3 {
		t.Fatalf("map holds %d documents, want the owner's 3", len(known))
	}
	for _, id := range []string{f.docBoth, f.docOne, f.docNone} {
		if known[toNgxID(id)] != id {
			t.Fatalf("%s is missing from the owner's map", id)
		}
	}
	if _, leaked := known[toNgxID(f.docTheir)]; leaked {
		t.Fatal("another owner's document is in the map")
	}
}

func TestNgxIDCacheEvictsWhenFull(t *testing.T) {
	t.Parallel()
	cache := &ngxIDCache{scopes: map[ngxIDScope]map[int]string{}}
	for i := 0; i < maxNgxIDScopes+10; i++ {
		cache.put(ngxIDScope{"documents", fmt.Sprint(i)}, map[int]string{1: "pb"})
	}
	if got := len(cache.scopes); got > maxNgxIDScopes {
		t.Fatalf("cache holds %d scopes, want at most %d", got, maxNgxIDScopes)
	}
	last := ngxIDScope{"documents", fmt.Sprint(maxNgxIDScopes + 9)}
	if _, ok := cache.get(last, 1); !ok {
		t.Fatal("the most recent scope was evicted")
	}
}

// resetNgxIDCache isolates a test from resolutions other tests warmed, since
// the cache outlives any one app.
func resetNgxIDCache(t *testing.T) {
	t.Helper()
	ngxIDScopes.mu.Lock()
	ngxIDScopes.scopes = map[ngxIDScope]map[int]string{}
	ngxIDScopes.mu.Unlock()
}

// TestNewRecordIsReachableAfterTheMapWasCached is the risk of caching the map
// whole rather than one entry: the map claims to know every id, so a document
// created after it was built must still resolve. It does because a miss always
// rescans -- only hits are served from the cache.
func TestNewRecordIsReachableAfterTheMapWasCached(t *testing.T) {
	f := newListFixture(t)
	resetNgxIDCache(t)

	// Build and cache the map.
	if _, err := findRecordByNgxID(f.app, "documents", toNgxID(f.docOne), f.userID); err != nil {
		t.Fatalf("warm the cache: %v", err)
	}

	fresh := createDocument(t, f.app, f.userID, "Fresh", "2026-01-01", nil)
	record, err := findRecordByNgxID(f.app, "documents", toNgxID(fresh), f.userID)
	if err != nil {
		t.Fatalf("a document created after the map was cached did not resolve: %v", err)
	}
	if record.Id != fresh {
		t.Fatalf("resolved to %s, want %s", record.Id, fresh)
	}
}
