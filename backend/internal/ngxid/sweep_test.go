package ngxid_test

import (
	"testing"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/core"

	"lemmary/backend/internal/ngxid"
	_ "lemmary/backend/migrations"
)

// An external test package: these need the schema, and the migrations that
// build it import ngxid.

func bootApp(t *testing.T) *pocketbase.PocketBase {
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

func makeUser(t *testing.T, app core.App) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		t.Fatalf("users collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("email", "owner@example.com")
	record.SetPassword("test-password-123")
	if err := app.Save(record); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return record.Id
}

func makeTag(t *testing.T, app core.App, userID, name string) string {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("tags collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("name", name)
	if collection.Fields.GetByName("name_original") != nil {
		record.Set("name_original", name)
	}
	if err := app.Save(record); err != nil {
		t.Fatalf("save tag %s: %v", name, err)
	}
	return record.Id
}

func unstamp(t *testing.T, app core.App, collection, pbID string) {
	t.Helper()
	_, err := app.DB().NewQuery("UPDATE {{" + collection + "}} SET [[ngx_id]] = 0 WHERE [[id]] = {:id}").
		Bind(dbx.Params{"id": pbID}).Execute()
	if err != nil {
		t.Fatalf("clear the column on %s: %v", pbID, err)
	}
}

func storedID(t *testing.T, app core.App, collection, pbID string) int {
	t.Helper()
	record, err := app.FindRecordById(collection, pbID)
	if err != nil {
		t.Fatalf("load %s %s: %v", collection, pbID, err)
	}
	return record.GetInt(ngxid.Field)
}

// TestSweepStampsRowsThatCarryNone is what the migration backfill cannot reach:
// roll back to a build without these hooks, keep writing records, roll forward,
// and the migration is already recorded as applied.
func TestSweepStampsRowsThatCarryNone(t *testing.T) {
	app := bootApp(t)
	ngxid.Register(app)
	userID := makeUser(t, app)
	stranded := makeTag(t, app, userID, "stranded")
	kept := makeTag(t, app, userID, "kept")

	keptID := storedID(t, app, "tags", kept)
	unstamp(t, app, "tags", stranded)

	if err := ngxid.Sweep(app); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if got, want := storedID(t, app, "tags", stranded), ngxid.Hash(stranded); got != want {
		t.Fatalf("the stranded tag was numbered %d, want the derived %d", got, want)
	}
	if got := storedID(t, app, "tags", kept); got != keptID {
		t.Fatalf("sweep renumbered a tag that already had an id: %d, was %d", got, keptID)
	}
}

// TestUpdateRepairsAMissingID is the same repair without waiting for a restart.
func TestUpdateRepairsAMissingID(t *testing.T) {
	app := bootApp(t)
	ngxid.Register(app)
	userID := makeUser(t, app)
	tag := makeTag(t, app, userID, "stranded")
	unstamp(t, app, "tags", tag)

	record, err := app.FindRecordById("tags", tag)
	if err != nil {
		t.Fatalf("load tag: %v", err)
	}
	record.Set("name", "renamed")
	if err := app.Save(record); err != nil {
		t.Fatalf("save tag: %v", err)
	}

	if got, want := storedID(t, app, "tags", tag), ngxid.Hash(tag); got != want {
		t.Fatalf("the tag was numbered %d after an update, want the derived %d", got, want)
	}
}

// TestUpdateKeepsTheIDItWasIssued: an id is permanent, so a PATCH carrying a
// different one must not repoint a client's cached id at another record.
func TestUpdateKeepsTheIDItWasIssued(t *testing.T) {
	app := bootApp(t)
	ngxid.Register(app)
	userID := makeUser(t, app)
	tag := makeTag(t, app, userID, "keep")
	issued := storedID(t, app, "tags", tag)

	record, err := app.FindRecordById("tags", tag)
	if err != nil {
		t.Fatalf("load tag: %v", err)
	}
	record.Set(ngxid.Field, issued+1)
	if err := app.Save(record); err != nil {
		t.Fatalf("save tag: %v", err)
	}

	if got := storedID(t, app, "tags", tag); got != issued {
		t.Fatalf("the tag now answers to %d, want the id it was issued, %d", got, issued)
	}
}

// TestAssignSkipsACollectionWithoutTheColumn is why an upgrade from before the
// column still boots: 1730000011 clones a tag per owner, and probing there
// would query a column no migration has added yet.
func TestAssignSkipsACollectionWithoutTheColumn(t *testing.T) {
	app := bootApp(t)
	dropNgxIDColumn(t, app, "tags")
	ngxid.Register(app)

	userID := makeUser(t, app)
	collection, err := app.FindCollectionByNameOrId("tags")
	if err != nil {
		t.Fatalf("tags collection: %v", err)
	}
	record := core.NewRecord(collection)
	record.Set("user", userID)
	record.Set("name", "pre-column")
	if collection.Fields.GetByName("name_original") != nil {
		record.Set("name_original", "pre-column")
	}
	if err := app.Save(record); err != nil {
		t.Fatalf("creating a record before the column exists must succeed: %v", err)
	}
}

func dropNgxIDColumn(t *testing.T, app core.App, name string) {
	t.Helper()
	collection, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		t.Fatalf("%s collection: %v", name, err)
	}
	collection.RemoveIndex("idx_" + name + "_" + ngxid.Field)
	field := collection.Fields.GetByName(ngxid.Field)
	if field == nil {
		t.Fatalf("%s already has no %s field", name, ngxid.Field)
	}
	collection.Fields.RemoveById(field.GetId())
	if err := app.Save(collection); err != nil {
		t.Fatalf("drop %s.%s: %v", name, ngxid.Field, err)
	}
}
