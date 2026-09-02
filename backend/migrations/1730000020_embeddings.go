package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"

	"lemmary/backend/internal/embedstore"
)

// The fifth task binding, plus the raw tables the vectors live in.
//
// embedding_dims is stored rather than configured: only the provider knows how
// long its vectors are, and the number has to survive a restart so the vector
// index can be sized before any embedding call is made. It is read-only in the
// Settings API for the same reason -- an admin typing 1536 next to a 3072-
// dimension model would produce an index that silently drops every vector.
//
// The tables are created here rather than lazily on first use so that a
// migrated instance has them whether or not embeddings are ever turned on:
// EnsureSchema is idempotent, but a table that appears halfway through an
// instance's life is a table the vault's snapshot and PocketBase's backup had
// no reason to expect.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("embedding_provider_id") == nil {
			collection.Fields.Add(&core.TextField{Name: "embedding_provider_id", Max: 15})
		}
		if collection.Fields.GetByName("embedding_model") == nil {
			collection.Fields.Add(&core.TextField{Name: "embedding_model", Max: 200})
		}
		if collection.Fields.GetByName("embedding_dims") == nil {
			collection.Fields.Add(&core.NumberField{Name: "embedding_dims", OnlyInt: true, Min: types.Pointer(0.0)})
		}
		if err := app.Save(collection); err != nil {
			return err
		}
		return embedstore.EnsureSchema(app.DB())
	}, func(app core.App) error {
		if err := embedstore.DropSchema(app.DB()); err != nil {
			return err
		}
		collection, err := app.FindCollectionByNameOrId("app_settings")
		if err != nil {
			return nil
		}
		for _, name := range []string{"embedding_provider_id", "embedding_model", "embedding_dims"} {
			if f := collection.Fields.GetByName(name); f != nil {
				collection.Fields.RemoveById(f.GetId())
			}
		}
		return app.Save(collection)
	})
}
