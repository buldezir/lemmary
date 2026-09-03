package ngxapi

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ngxid"
)

// countDocumentQueries records every SELECT against the documents table so a
// test can assert how many the lookup actually costs.
func countDocumentQueries(t *testing.T, app *pocketbase.PocketBase) func() int {
	t.Helper()
	return countTableQueries(t, app, "documents")
}

func countTableQueries(t *testing.T, app *pocketbase.PocketBase, table string) func() int {
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
			if strings.Contains(query, "`"+table+"`") {
				n++
			}
		}
		seen = nil
		return n
	}
}

// TestThumbnailGridCostsOneQueryPerTile is the traffic that forced the stored
// id: a page of thumbnails is one request per tile, each naming a *different*
// id, and each one used to scan the whole archive to invert an FNV hash.
func TestThumbnailGridCostsOneQueryPerTile(t *testing.T) {
	f := newListFixture(t)

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

	if n, want := queries(), 3; n != want {
		t.Fatalf("three lookups ran %d document queries, want %d", n, want)
	}
}

// TestReverseLookupUsesTheIndex is what the query count above cannot prove: a
// count of one is a seek or a full scan, and only the plan says which. Losing
// the index -- or writing a filter SQLite cannot match against a partial one --
// would put the thumbnail grid straight back to scanning per tile.
//
// It explains the SQL the lookup actually issued rather than a hand-written
// equivalent, because the filter goes through PocketBase's compiler and it is
// that output the planner sees.
func TestReverseLookupUsesTheIndex(t *testing.T) {
	f := newListFixture(t)

	sql := captureDocumentSelect(t, f.app, func() {
		if _, err := findRecordByNgxID(f.app, "documents", toNgxID(f.docOne), f.userID); err != nil {
			t.Fatalf("lookup: %v", err)
		}
	})

	var plan []struct {
		Detail string `db:"detail"`
	}
	if err := f.app.DB().NewQuery("EXPLAIN QUERY PLAN " + sql).All(&plan); err != nil {
		t.Fatalf("explain %q: %v", sql, err)
	}

	joined := ""
	for _, step := range plan {
		joined += step.Detail + "\n"
	}
	if !strings.Contains(joined, "idx_documents_ngx_id") {
		t.Fatalf("the reverse lookup is not using its index:\n%s\nfor: %s", joined, sql)
	}
}

// captureDocumentSelect returns the SELECT against documents that run issued,
// with its bound parameters already inlined by the driver logger.
func captureDocumentSelect(t *testing.T, app *pocketbase.PocketBase, run func()) string {
	t.Helper()
	db, ok := app.ConcurrentDB().(*dbx.DB)
	if !ok {
		t.Skip("ConcurrentDB is not a *dbx.DB on this PocketBase build")
	}

	var (
		mu      sync.Mutex
		queries []string
	)
	previous := db.QueryLogFunc
	db.QueryLogFunc = func(ctx context.Context, d time.Duration, query string, rows *sql.Rows, err error) {
		mu.Lock()
		queries = append(queries, query)
		mu.Unlock()
		if previous != nil {
			previous(ctx, d, query, rows, err)
		}
	}
	defer func() { db.QueryLogFunc = previous }()

	run()

	mu.Lock()
	defer mu.Unlock()
	for _, query := range queries {
		if strings.Contains(query, "`documents`") && strings.Contains(query, "ngx_id") {
			return query
		}
	}
	t.Fatalf("no documents lookup was issued, saw: %v", queries)
	return ""
}

// TestDerivedIDStillResolves is the upgrade promise. The stored id is seeded
// from the hash that used to be computed on the fly, so a paperless client
// holding ids from before the column -- or a thumbnail cached under a URL
// containing one -- keeps pointing at the same document.
func TestDerivedIDStillResolves(t *testing.T) {
	f := newListFixture(t)

	record, err := f.app.FindRecordById("documents", f.docOne)
	if err != nil {
		t.Fatalf("load document: %v", err)
	}
	if got, want := ngxIDOf(record), ngxid.Hash(f.docOne); got != want {
		t.Fatalf("stored id = %d, want the derived %d", got, want)
	}
}

// TestDeletedDocumentStopsResolving: a deleted document must stop resolving, or
// it would keep serving its file.
func TestDeletedDocumentStopsResolving(t *testing.T) {
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
		t.Fatal("a deleted document still resolved")
	}
}

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

// TestZeroIsNotAnID pins the one value the column can hold without a hook ever
// having stamped it. Resolving it would hand a client a record for asking about
// nothing.
func TestZeroIsNotAnID(t *testing.T) {
	f := newListFixture(t)
	if _, err := findRecordByNgxID(f.app, "documents", 0, f.userID); err == nil {
		t.Fatal("id 0 resolved to a record")
	}
}

// TestEveryAddressableCollectionIsStamped: a record with no client id is
// invisible to every paperless client, so the hook has to cover all four.
func TestEveryAddressableCollectionIsStamped(t *testing.T) {
	app := bootSchemaTestApp(t)
	userID := createUser(t, app, "stamped@example.com")

	documentID := createDocument(t, app, userID, "Stamped", "2025-01-01", nil)

	for _, collection := range ngxid.Names() {
		var pbID string
		switch collection {
		case "documents":
			pbID = documentID
		case "processing_jobs":
			pbID = createJob(t, app, documentID, "pending")
		default:
			pbID = createNamed(t, app, collection, "stamped", userID)
		}
		record, err := app.FindRecordById(collection, pbID)
		if err != nil {
			t.Fatalf("load %s: %v", collection, err)
		}
		if got := ngxIDOf(record); got <= 0 {
			t.Fatalf("%s was created with ngx_id %d", collection, got)
		}
	}
}

// TestCollidingHashTakesTheNextFreeID is the bug the column fixes rather than
// inherits. Two of an owner's records hashing alike used to leave the second
// one permanently unreachable through the paperless API -- and at 31 bits an
// owner with 50k documents has roughly even odds of holding such a pair.
func TestCollidingHashTakesTheNextFreeID(t *testing.T) {
	app := bootSchemaTestApp(t)
	userID := createUser(t, app, "collide@example.com")

	// The colliding id is chosen up front so the squatter can be parked on it
	// before the record that wants it exists.
	const wantedPBID = "collidingtag001"
	collided := ngxid.Hash(wantedPBID)

	squatter := createNamed(t, app, "tags", "squatter", userID)
	if _, err := app.DB().NewQuery("UPDATE {{tags}} SET [[ngx_id]] = {:v} WHERE [[id]] = {:id}").
		Bind(dbx.Params{"v": collided, "id": squatter}).Execute(); err != nil {
		t.Fatalf("park the squatter: %v", err)
	}

	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("tags collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Id = wantedPBID
	record.Set("name", "collider")
	record.Set("name_original", "collider")
	record.Set("user", userID)
	if err := app.Save(record); err != nil {
		t.Fatalf("save the colliding tag: %v", err)
	}

	if got := ngxIDOf(record); got != collided+1 {
		t.Fatalf("colliding tag took id %d, want the next free %d", got, collided+1)
	}
	found, err := findRecordByNgxID(app, "tags", collided+1, userID)
	if err != nil {
		t.Fatalf("the probed id does not resolve: %v", err)
	}
	if found.Id != wantedPBID {
		t.Fatalf("resolved to %s, want %s", found.Id, wantedPBID)
	}
}

// TestOwnersDoNotCollideWithEachOther: uniqueness is per owner, matching how
// every lookup is scoped. Two owners sharing an id was always reachable and
// must stay so, or an upgrade would renumber records for no reason.
func TestOwnersDoNotCollideWithEachOther(t *testing.T) {
	app := bootSchemaTestApp(t)
	mine := createUser(t, app, "mine@example.com")
	theirs := createUser(t, app, "theirs@example.com")

	myTag := createNamed(t, app, "tags", "shared", mine)
	theirTag := createNamed(t, app, "tags", "shared", theirs)

	shared := ngxIDOf(mustFind(t, app, "tags", myTag))
	if _, err := app.DB().NewQuery("UPDATE {{tags}} SET [[ngx_id]] = {:v} WHERE [[id]] = {:id}").
		Bind(dbx.Params{"v": shared, "id": theirTag}).Execute(); err != nil {
		t.Fatalf("share the id across owners: %v", err)
	}

	for _, tc := range []struct{ owner, want string }{{mine, myTag}, {theirs, theirTag}} {
		found, err := findRecordByNgxID(app, "tags", shared, tc.owner)
		if err != nil {
			t.Fatalf("lookup for %s: %v", tc.owner, err)
		}
		if found.Id != tc.want {
			t.Fatalf("resolved to %s, want %s", found.Id, tc.want)
		}
	}
}

func mustFind(t *testing.T, app core.App, collection, pbID string) *core.Record {
	t.Helper()
	record, err := app.FindRecordById(collection, pbID)
	if err != nil {
		t.Fatalf("load %s %s: %v", collection, pbID, err)
	}
	return record
}

// TestIDSurvivesAnUpdate: the id is permanent once issued -- swift-paperless
// keys its thumbnail cache on a URL containing it -- so an update carrying a
// different value, or none, must not move it.
func TestIDSurvivesAnUpdate(t *testing.T) {
	f := newListFixture(t)

	record := mustFind(t, f.app, "documents", f.docOne)
	issued := ngxIDOf(record)

	record.Set(ngxid.Field, 0)
	record.Set("title", "Renamed")
	if err := f.app.Save(record); err != nil {
		t.Fatalf("save: %v", err)
	}

	if got := ngxIDOf(mustFind(t, f.app, "documents", f.docOne)); got != issued {
		t.Fatalf("id changed to %d on update, want the issued %d", got, issued)
	}
	if _, err := findRecordByNgxID(f.app, "documents", issued, f.userID); err != nil {
		t.Fatalf("the document stopped resolving after an update: %v", err)
	}
}
