package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/chat"
)

// A research turn is now bounded by the provider alone, so how close it came to
// that bound is worth showing. The live stream reports it while the run is on;
// this column is what a reopened chat reads it back from. Assistant rows only,
// and absent on every turn taken before this.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("usage") == nil {
			collection.Fields.Add(&core.JSONField{Name: "usage", MaxSize: chat.MaxUsageJSONBytes})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
		if err != nil {
			return nil
		}
		if field := collection.Fields.GetByName("usage"); field != nil {
			collection.Fields.RemoveById(field.GetId())
		}
		return app.Save(collection)
	})
}
