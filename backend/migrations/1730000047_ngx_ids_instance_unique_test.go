package migrations

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ngxid"
)

func setNgxID(t *testing.T, app core.App, collection, pbID string, value int) {
	t.Helper()
	_, err := app.DB().NewQuery("UPDATE {{" + collection + "}} SET [[ngx_id]] = {:value} WHERE [[id]] = {:id}").
		Bind(dbx.Params{"value": value, "id": pbID}).Execute()
	if err != nil {
		t.Fatalf("set ngx_id: %v", err)
	}
}

// The upgrade has to survive a volume that already holds a collision, which is
// exactly what the old per-owner constraint allowed.
func TestInstanceUniqueNgxIDsRenumberAnExistingCollision(t *testing.T) {
	app := bootMigratedApp(t)
	mine := makeUser(t, app, "mine@example.com")
	theirs := makeUser(t, app, "theirs@example.com")
	older := makeDocument(t, app, mine, "Mine")
	newer := makeDocument(t, app, theirs, "Theirs")
	if older > newer {
		older, newer = newer, older
	}

	// Recreate the pre-upgrade state: the same id under two owners, which the
	// new index would refuse.
	if err := indexNgxID(app, "documents", "user"); err != nil {
		t.Fatalf("restore the per-owner index: %v", err)
	}
	setNgxID(t, app, "documents", older, 4242)
	setNgxID(t, app, "documents", newer, 4242)

	if err := clearDuplicateNgxIDs(app, "documents"); err != nil {
		t.Fatalf("clear duplicates: %v", err)
	}
	if err := indexNgxID(app, "documents", ""); err != nil {
		t.Fatalf("index: %v", err)
	}
	if err := ngxid.Sweep(app); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	kept := storedNgxID(t, app, "documents", older)
	moved := storedNgxID(t, app, "documents", newer)
	if kept != 4242 {
		t.Fatalf("the older document lost its id: got %d, want 4242", kept)
	}
	if moved == kept || moved == 0 {
		t.Fatalf("the colliding document was not renumbered: got %d", moved)
	}
}

func TestInstanceUniqueNgxIDsLeaveUncontestedIDsAlone(t *testing.T) {
	app := bootMigratedApp(t)
	mine := makeUser(t, app, "mine@example.com")
	theirs := makeUser(t, app, "theirs@example.com")
	myDoc := makeDocument(t, app, mine, "Mine")
	theirDoc := makeDocument(t, app, theirs, "Theirs")

	before := map[string]int{
		myDoc:    storedNgxID(t, app, "documents", myDoc),
		theirDoc: storedNgxID(t, app, "documents", theirDoc),
	}
	if err := clearDuplicateNgxIDs(app, "documents"); err != nil {
		t.Fatalf("clear duplicates: %v", err)
	}
	for id, want := range before {
		if got := storedNgxID(t, app, "documents", id); got != want {
			t.Fatalf("an uncontested id changed: got %d, want %d", got, want)
		}
	}
}

// A managed instance re-runs every migration on every boot.
func TestInstanceUniqueNgxIDsMigrationIsIdempotent(t *testing.T) {
	app := bootMigratedApp(t)
	owner := makeUser(t, app, "owner@example.com")
	docID := makeDocument(t, app, owner, "Doc")
	// testpb binds no hooks, so the row arrives unstamped: sweep once to reach
	// the state a real boot leaves behind, then re-run the whole migration.
	if err := ngxid.Sweep(app); err != nil {
		t.Fatalf("first sweep: %v", err)
	}
	want := storedNgxID(t, app, "documents", docID)
	if want == 0 {
		t.Fatal("the fixture document was never stamped")
	}

	for _, collection := range ngxIDsV40 {
		if err := clearDuplicateNgxIDs(app, collection); err != nil {
			t.Fatalf("clear duplicates in %s: %v", collection, err)
		}
		if err := indexNgxID(app, collection, ""); err != nil {
			t.Fatalf("index %s: %v", collection, err)
		}
	}
	if err := ngxid.Sweep(app); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if got := storedNgxID(t, app, "documents", docID); got != want {
		t.Fatalf("a re-run renumbered a document: got %d, want %d", got, want)
	}
}
