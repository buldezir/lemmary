package migrations

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"

	"lemmary/backend/internal/ngxid"
)

func bootMigratedApp(t *testing.T) *pocketbase.PocketBase {
	t.Helper()
	app := pocketbase.NewWithConfig(pocketbase.Config{
		DefaultDataDir:  t.TempDir(),
		HideStartBanner: true,
	})
	if err := app.Bootstrap(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.ResetBootstrapState() })
	if err := app.RunAppMigrations(); err != nil {
		t.Fatalf("run app migrations: %v", err)
	}
	return app
}

func makeUser(t *testing.T, app core.App, email string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("email", email)
	record.SetPassword("test-password-123")
	if err := app.Save(record); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return record.Id
}

func makeDocument(t *testing.T, app core.App, userID, title string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		t.Fatalf("documents collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("title", title)
	file, err := filesystem.NewFileFromBytes([]byte("document "+title), title+".txt")
	if err != nil {
		t.Fatalf("build file: %v", err)
	}
	record.Set("file", file)
	if err := app.Save(record); err != nil {
		t.Fatalf("save document %s: %v", title, err)
	}
	return record.Id
}

func storedNgxID(t *testing.T, app core.App, collection, pbID string) int {
	t.Helper()
	record, err := app.FindRecordById(collection, pbID)
	if err != nil {
		t.Fatalf("load %s %s: %v", collection, pbID, err)
	}
	return record.GetInt(ngxid.Field)
}

// TestBackfillNumbersRowsThatPredateTheColumn is the upgrade path. The rows an
// existing install already has were created before anything stamped them, and
// an unstamped row is invisible to every paperless client.
//
// The column is zeroed first because the migration has already run by the time
// a test app is up: that is what an upgrading install actually looks like at
// the moment the backfill starts.
func TestBackfillNumbersRowsThatPredateTheColumn(t *testing.T) {
	app := bootMigratedApp(t)
	userID := makeUser(t, app, "upgrade@example.com")
	first := makeDocument(t, app, userID, "First")
	second := makeDocument(t, app, userID, "Second")

	clearNgxIDs(t, app)
	if err := backfillNgxIDs(app, "documents", "user"); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	for _, pbID := range []string{first, second} {
		if got, want := storedNgxID(t, app, "documents", pbID), ngxid.Hash(pbID); got != want {
			t.Fatalf("%s was numbered %d, want the derived %d", pbID, got, want)
		}
	}
}

// TestBackfillResolvesCollisionsForward: two of an owner's rows can hash alike,
// and the row that used to win that tie -- the derived lookup broke ties toward
// the lowest PocketBase id -- has to keep the id it was already answering to,
// or the upgrade silently repoints a client's cached id at a different
// document.
//
// The seed is forced rather than found: a real collision is a 31-bit birthday
// event and cannot be built from a fixture.
func TestBackfillResolvesCollisionsForward(t *testing.T) {
	app := bootMigratedApp(t)
	userID := makeUser(t, app, "collide@example.com")

	// Ids chosen so the walk order is unambiguous -- the backfill numbers rows
	// in ascending PocketBase id, which is the tie-break the derived lookup
	// used.
	const lower, higher = "aaaaaaaaaaaaaaa", "zzzzzzzzzzzzzzz"
	renameDocument(t, app, makeDocument(t, app, userID, "Lower"), lower)
	renameDocument(t, app, makeDocument(t, app, userID, "Higher"), higher)

	const seed = 777_000
	clearNgxIDs(t, app)
	if err := backfillNgxIDsSeeded(app, "documents", "user", func(string) int { return seed }); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if got := storedNgxID(t, app, "documents", lower); got != seed {
		t.Fatalf("the lower id lost the contested value: got %d, want %d", got, seed)
	}
	if got := storedNgxID(t, app, "documents", higher); got != seed+1 {
		t.Fatalf("the higher id took %d, want the next free %d", got, seed+1)
	}
}

func renameDocument(t *testing.T, app core.App, from, to string) {
	t.Helper()
	if _, err := app.DB().NewQuery("UPDATE {{documents}} SET [[id]] = {:to} WHERE [[id]] = {:from}").
		Bind(dbx.Params{"to": to, "from": from}).Execute(); err != nil {
		t.Fatalf("rename %s: %v", from, err)
	}
}

func clearNgxIDs(t *testing.T, app core.App) {
	t.Helper()
	if _, err := app.DB().NewQuery("UPDATE {{documents}} SET [[ngx_id]] = 0").Execute(); err != nil {
		t.Fatalf("clear the column: %v", err)
	}
}

// TestBackfillKeepsOwnersApart: uniqueness is per owner, so two owners hashing
// alike is not a collision and must not renumber either of them.
func TestBackfillKeepsOwnersApart(t *testing.T) {
	app := bootMigratedApp(t)
	mine := makeUser(t, app, "mine@example.com")
	theirs := makeUser(t, app, "theirs@example.com")
	myDoc := makeDocument(t, app, mine, "Mine")
	theirDoc := makeDocument(t, app, theirs, "Theirs")

	clearNgxIDs(t, app)
	if err := backfillNgxIDsSeeded(app, "documents", "user", func(string) int { return 4242 }); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	if got := storedNgxID(t, app, "documents", myDoc); got != 4242 {
		t.Fatalf("my document took %d, want 4242", got)
	}
	if got := storedNgxID(t, app, "documents", theirDoc); got != 4242 {
		t.Fatalf("the other owner's document took %d, want the same 4242", got)
	}
}
