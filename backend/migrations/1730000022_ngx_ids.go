package migrations

import (
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/ngxid"
)

// Stores the integer id paperless-ngx clients address records by, instead of
// deriving it from a hash of the PocketBase id on every request.
//
// The hash was one-way, so answering "which document is 944698583" meant
// hashing every row the owner has. That is once per request, and the request
// that dominates is a thumbnail: swift-paperless prefetches a preview for the
// whole page it fetched -- 250 documents by default, all at once -- so opening
// the app was 250 scans of the archive. As a column it is an index seek.
//
// Seeded from the same hash so no client-visible id changes: a paperless client
// that cached document ids, or a thumbnail keyed on a URL containing one, is
// still pointing at the same record after the upgrade. Where two of an owner's
// rows hash alike the second one through takes the next free id -- which is a
// fix, not a change: the shadowed row used to be unreachable through the
// paperless API entirely, and an owner with 50k documents has roughly even odds
// of holding such a pair.
// ngxIDsV22 is frozen rather than read from ngxid.Collections: a migration
// describes one fixed transition, and a collection added to that list later
// gets its own migration rather than silently changing what this one did.
var ngxIDsV22 = []string{"documents", "tags", "correspondents", "document_types"}

func init() {
	m.Register(func(app core.App) error {
		// Three passes rather than one, so a bug in the backfill is a failed
		// migration instead of a duplicate id: the column arrives first, the
		// values second, and the constraint that proves them last.
		for _, collection := range ngxIDsV22 {
			if err := addNgxIDField(app, collection); err != nil {
				return err
			}
		}
		for _, collection := range ngxIDsV22 {
			if err := backfillNgxIDs(app, collection, "user"); err != nil {
				return err
			}
		}
		for _, collection := range ngxIDsV22 {
			if err := indexNgxID(app, collection, "user"); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		for _, collection := range ngxIDsV22 {
			coll, err := app.FindCollectionByNameOrId(collection)
			if err != nil {
				continue
			}
			coll.RemoveIndex(ngxIDIndexName(collection))
			if f := coll.Fields.GetByName(ngxid.Field); f != nil {
				coll.Fields.RemoveById(f.GetId())
			}
			if err := app.Save(coll); err != nil {
				return err
			}
		}
		return nil
	})
}

func ngxIDIndexName(collection string) string {
	return "idx_" + collection + "_" + ngxid.Field
}

func addNgxIDField(app core.App, collection string) error {
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		return err
	}
	if coll.Fields.GetByName(ngxid.Field) != nil {
		return nil
	}
	coll.Fields.Add(&core.NumberField{
		Name:    ngxid.Field,
		OnlyInt: true,
		Min:     types.Pointer(1.0),
		Max:     types.Pointer(float64(ngxid.Max)),
		// Hidden so an owner cannot rewrite it through the collection API. The
		// rules on these collections let an owner patch their own rows, and a
		// PATCH carrying ngx_id would either repoint one of their paperless
		// client's cached ids at a different document or trip the unique index
		// with an error that explains nothing.
		Hidden: true,
		// Deliberately not Required. Every production create path goes through
		// ngxid.Register, but a unit test in another package boots a bare app
		// with no hooks bound, and making the column mandatory would turn every
		// such fixture into a failure over an id that test will never read. The
		// index below is partial for the same reason.
		Required: false,
	})
	return app.Save(coll)
}

// backfillNgxIDs numbers the rows that predate the column.
//
// Ordered by PocketBase id, and colliding rows resolve forward, so the row that
// used to win a collision -- the derived lookup broke ties toward the lowest
// PocketBase id -- keeps the id it was already answering to.
//
// Raw SQL rather than app.Save: saving a document fires the record hooks, which
// would rebuild its search index entry and mark its embeddings stale once per
// row, at boot, for the entire archive.
func backfillNgxIDs(app core.App, collection, ownerField string) error {
	return backfillNgxIDsSeeded(app, collection, ownerField, ngxid.Hash)
}

// backfillNgxIDsSeeded is backfillNgxIDs with the seed handed in, which is the
// only way to test the collision walk: real collisions are 31-bit birthday
// events and cannot be constructed from a fixture.
func backfillNgxIDsSeeded(app core.App, collection, ownerField string, seed func(pbID string) int) error {
	type row struct {
		ID    string `db:"id"`
		Owner string `db:"owner"`
	}
	owner := "''"
	if ownerField != "" {
		owner = "[[" + ownerField + "]]"
	}
	var rows []row
	query := fmt.Sprintf("SELECT [[id]], %s AS [[owner]] FROM {{%s}} ORDER BY [[id]] ASC", owner, collection)
	if err := app.DB().NewQuery(query).All(&rows); err != nil {
		return err
	}

	update := fmt.Sprintf("UPDATE {{%s}} SET [[%s]] = {:value} WHERE [[id]] = {:id}", collection, ngxid.Field)
	taken := map[string]map[int]bool{}
	for _, r := range rows {
		owned := taken[r.Owner]
		if owned == nil {
			owned = map[int]bool{}
			taken[r.Owner] = owned
		}
		id := ngxid.Free(seed(r.ID), func(candidate int) bool { return owned[candidate] })
		if id == 0 {
			return fmt.Errorf("no free ngx id for %s %s", collection, r.ID)
		}
		owned[id] = true
		if _, err := app.DB().NewQuery(update).Bind(dbx.Params{"value": id, "id": r.ID}).Execute(); err != nil {
			return err
		}
	}
	return nil
}

// indexNgxID is what makes the reverse lookup a seek, and what stops two of an
// owner's records ever sharing an id.
//
// Partial, on ngx_id > 0: the column defaults to 0, and a row created by a path
// with no hooks bound would otherwise collide with the next such row rather
// than simply being absent from the paperless API.
func indexNgxID(app core.App, collection, ownerField string) error {
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		return err
	}
	columns := ngxid.Field
	if ownerField != "" {
		columns = ownerField + ", " + ngxid.Field
	}
	coll.AddIndex(ngxIDIndexName(collection), true, columns, ngxid.Field+" > 0")
	return app.Save(coll)
}
