package migrations

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/ngxid"
)

// Client-facing ids were unique per owner, which was the right scope while every
// read was too. A shared document breaks that: the caller now reads rows two
// owners hold, and if both hold id 944698583 the shared one resolves to the
// reader's own document instead of failing — the worst shape a bug can take.
//
// Widening the constraint renumbers only the rows that actually collide, and a
// colliding row was already unreachable for one of the two owners.
//
// Frozen rather than read from ngxid.Collections, like 1730000022: a migration
// describes one fixed transition.
var ngxIDsV40 = []string{"documents", "tags", "correspondents", "document_types"}

func init() {
	m.Register(func(app core.App) error {
		for _, collection := range ngxIDsV40 {
			if err := clearDuplicateNgxIDs(app, collection); err != nil {
				return err
			}
			if err := indexNgxID(app, collection, ""); err != nil {
				return err
			}
		}
		// Restamps everything just cleared, now probing the whole table.
		return ngxid.Sweep(app)
	}, func(app core.App) error {
		for _, collection := range ngxIDsV40 {
			if err := indexNgxID(app, collection, "user"); err != nil {
				return err
			}
		}
		return nil
	})
}

// clearDuplicateNgxIDs zeroes every row but one of each duplicated id, so the
// unique index can be created and Sweep can hand the rest a free one. The row
// kept is the one with the smallest PocketBase id, which is the oldest: whoever
// held the id first keeps it, and the client that already cached it stays right.
func clearDuplicateNgxIDs(app core.App, collection string) error {
	coll, err := app.FindCollectionByNameOrId(collection)
	if err != nil {
		return nil
	}
	if coll.Fields.GetByName(ngxid.Field) == nil {
		return nil
	}
	query := fmt.Sprintf(
		"UPDATE {{%s}} AS a SET [[%s]] = 0 WHERE COALESCE(a.[[%s]], 0) > 0 AND EXISTS ("+
			"SELECT 1 FROM {{%s}} b WHERE b.[[%s]] = a.[[%s]] AND b.[[id]] < a.[[id]])",
		collection, ngxid.Field, ngxid.Field, collection, ngxid.Field, ngxid.Field,
	)
	_, err = app.DB().NewQuery(query).Execute()
	return err
}
