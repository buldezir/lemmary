// Package ngxid owns the integer ids Lemmary shows paperless-ngx clients.
//
// PocketBase ids are 15-character strings; the paperless-ngx REST API is
// specified in terms of integers, so every document, tag, correspondent and
// document type needs a second, numeric identity. It used to be derived on the
// spot -- an FNV-32a hash of the PocketBase id -- which made the forward
// direction free and the reverse direction impossible: inverting the hash meant
// hashing every candidate row. A thumbnail grid is one request per tile, each
// naming a different id, so a screen of twenty-five previews cost twenty-five
// scans of the archive.
//
// The id is stored instead. It is still seeded from the hash, so the ids clients
// already hold keep pointing at the same records across the upgrade, but it is a
// column now: reverse lookup is an index seek, and a hash collision no longer
// makes a record permanently unreachable -- the second record through takes the
// next free value.
package ngxid

import (
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
)

// Field is the column every collection in Collections carries.
const Field = "ngx_id"

// Max is the largest id handed out. paperless-ngx ids are Django AutoFields,
// which clients decode as signed 32-bit, so the top bit stays clear.
const Max = 0x7fffffff

// Collection is one collection whose records a paperless-ngx client addresses
// by integer id.
type Collection struct {
	Name string
	// Owner is the field the id is unique within, empty when the collection has
	// no owner column of its own. Scoping uniqueness the same way the API
	// scopes lookups is what let the upgrade renumber nothing: a collision
	// between two owners' hashes was never reachable in the first place.
	Owner string
}

// Collections are those collections. Users are absent deliberately: a client
// reads a user id and never sends it back, so it is still derived from the hash
// and needs no column.
var Collections = []Collection{
	{Name: "documents", Owner: "user"},
	{Name: "tags", Owner: "user"},
	{Name: "correspondents", Owner: "user"},
	{Name: "document_types", Owner: "user"},
	// A job belongs to an account only through its document, so its ids are
	// unique across the install rather than per owner. Reads are still scoped
	// to the caller, by joining that document.
	{Name: "processing_jobs", Owner: ""},
}

// Names is Collections for the APIs that take collection names.
func Names() []string {
	names := make([]string, 0, len(Collections))
	for _, c := range Collections {
		names = append(names, c.Name)
	}
	return names
}

// ownerFieldOf is the Owner of the named collection, and false when the
// collection carries no client-facing id at all.
func ownerFieldOf(name string) (string, bool) {
	for _, c := range Collections {
		if c.Name == name {
			return c.Owner, true
		}
	}
	return "", false
}

// maxProbes bounds the walk for a free id. Reaching it means an owner holds a
// run of that many consecutive taken ids, which cannot happen by hashing -- so
// it is a bug or a corrupted table, and failing the write says so instead of
// spinning.
const maxProbes = 1000

// Hash is the id a record would have had before the column existed.
//
// It stays the seed rather than a bare sequence so that a client which cached
// ids -- swift-paperless keys its thumbnail cache on the URL, which carries the
// document id -- keeps its cache warm across the upgrade.
func Hash(pbID string) int {
	if pbID == "" {
		return 0
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(pbID))
	id := int(h.Sum32() & Max)
	if id == 0 {
		return 1
	}
	return id
}

// Free returns the first id at or after start that taken reports unused,
// wrapping past Max back to 1. It returns 0 when maxProbes candidates were all
// taken.
func Free(start int, taken func(int) bool) int {
	if start < 1 {
		start = 1
	}
	id := start
	for i := 0; i < maxProbes; i++ {
		if !taken(id) {
			return id
		}
		if id >= Max {
			id = 1
		} else {
			id++
		}
	}
	return 0
}

// Register stamps Field on every new record in Collections.
//
// One hook is all of it, for the same reason limits.Register needs only two:
// every way a document or a tag comes into being ends in app.Save -- the SPA's
// collection API, the paperless-ngx post_document endpoint, a PDF split, an
// Amazon-orders import, a remote paperless pull, the superuser CLI and the
// PocketBase admin UI all pass through here.
//
// PocketBase's own OnRecordCreate handler binds at priority -99 and generates
// the record id inside it, so by the time this runs record.Id is set and there
// is something to hash.
func Register(app core.App) {
	app.OnRecordCreate(Names()...).BindFunc(func(e *core.RecordEvent) error {
		if err := Assign(e.App, e.Record); err != nil {
			return err
		}
		return e.Next()
	})

	// An id is permanent once issued: clients cache it, and swift-paperless
	// keys its thumbnail cache on a URL containing it. Marking the field Hidden
	// stops a regular account writing it, but only a regular account --
	// PocketBase's GrantSuperuserAccess is documented as allowing "changing all
	// system record fields, including those marked as Hidden", so without this
	// an admin PATCH could repoint a client's cached id at a different record,
	// or clear it and make the record invisible to every paperless client.
	//
	// An update is also the second chance at an id: a row still holding 0 is
	// unreachable through the paperless API, so it is stamped here.
	app.OnRecordUpdate(Names()...).BindFunc(func(e *core.RecordEvent) error {
		if Restore(e.Record); e.Record.GetInt(Field) == 0 {
			if err := Assign(e.App, e.Record); err != nil {
				return err
			}
		}
		return e.Next()
	})

	// The migration backfill runs once, so it does not cover rows written by a
	// rollback to a build without these hooks. OnServe rather than OnBootstrap:
	// pending migrations run inside the serve command, and this reads a column
	// they may still be adding.
	app.OnServe().BindFunc(func(e *core.ServeEvent) error {
		if err := Sweep(e.App); err != nil {
			// Those rows were already unreachable; refusing to serve helps
			// nobody.
			e.App.Logger().Error("ngxid: boot sweep failed", "error", err)
		}
		return e.Next()
	})
}

// Restore puts back the id a record was issued, discarding whatever an update
// carried. A record that never had one -- created by a path with no hooks bound
// -- is left alone so Assign can still be the thing that gives it one.
func Restore(record *core.Record) {
	original := record.Original().GetInt(Field)
	if original > 0 && record.GetInt(Field) != original {
		record.Set(Field, original)
	}
}

// Sweep stamps every row in Collections that carries no client-facing id.
//
// Raw SQL rather than app.Save: saving fires the record hooks, which would
// reindex and mark embeddings stale once per row, at boot.
func Sweep(app core.App) error {
	for _, c := range Collections {
		if err := sweepCollection(app, c); err != nil {
			return fmt.Errorf("ngxid: sweep %s: %w", c.Name, err)
		}
	}
	return nil
}

func sweepCollection(app core.App, c Collection) error {
	collection, err := app.FindCollectionByNameOrId(c.Name)
	if err != nil {
		return nil
	}
	if collection.Fields.GetByName(Field) == nil {
		return nil
	}

	type row struct {
		ID    string `db:"id"`
		Owner string `db:"owner"`
	}
	owner := "''"
	if c.Owner != "" {
		owner = "[[" + c.Owner + "]]"
	}
	var rows []row
	query := fmt.Sprintf(
		"SELECT [[id]], %s AS [[owner]] FROM {{%s}} WHERE COALESCE([[%s]], 0) = 0 ORDER BY [[id]] ASC",
		owner, c.Name, Field,
	)
	if err := app.DB().NewQuery(query).All(&rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	update := fmt.Sprintf("UPDATE {{%s}} SET [[%s]] = {:value} WHERE [[id]] = {:id}", c.Name, Field)
	// Each row is written before the next is probed, so the probe sees what
	// this loop has already handed out.
	for _, r := range rows {
		id, err := freeIDFor(app, c, r.Owner, Hash(r.ID))
		if err != nil {
			return err
		}
		if _, err := app.DB().NewQuery(update).Bind(dbx.Params{"value": id, "id": r.ID}).Execute(); err != nil {
			return err
		}
	}
	app.Logger().Info("ngxid: stamped records that carried no client-facing id",
		"collection", c.Name, "count", len(rows))
	return nil
}

// Assign gives record its client-facing id, leaving an id it already carries
// alone so a restore or an import can bring its own.
func Assign(app core.App, record *core.Record) error {
	if record.GetInt(Field) > 0 {
		return nil
	}
	if record.Id == "" {
		return errors.New("ngxid: record has no id to derive from")
	}

	// The hook binds before pending migrations run, and one of them creates
	// records (1730000011 clones a shared tag per owner). Probing there would
	// query a column no migration has added yet and take the boot down with it;
	// the backfill and Sweep number those rows instead.
	collection := record.Collection()
	if collection.Fields.GetByName(Field) == nil {
		return nil
	}

	name := collection.Name
	ownerField, known := ownerFieldOf(name)
	if !known {
		return fmt.Errorf("ngxid: %s carries no client-facing id", name)
	}

	id, err := freeIDFor(app, Collection{Name: name, Owner: ownerField}, record.GetString(ownerField), Hash(record.Id))
	if err != nil {
		return err
	}
	record.Set(Field, id)
	return nil
}

// freeIDFor walks from seed to the first id no record of this owner holds.
func freeIDFor(app core.App, c Collection, ownerID string, seed int) (int, error) {
	scope := dbx.HashExp{Field: nil}
	if c.Owner != "" {
		scope[c.Owner] = ownerID
	}

	var probeErr error
	id := Free(seed, func(candidate int) bool {
		if probeErr != nil {
			return false
		}
		scope[Field] = candidate
		n, err := app.CountRecords(c.Name, scope)
		if err != nil {
			probeErr = err
			return false
		}
		return n > 0
	})
	if probeErr != nil {
		return 0, probeErr
	}
	if id == 0 {
		return 0, fmt.Errorf("ngxid: no free id for %s near %d", c.Name, seed)
	}
	return id, nil
}
