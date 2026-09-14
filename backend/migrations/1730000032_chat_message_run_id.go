package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/chat"
)

// A client-generated run id ties a stored user/assistant pair to the request
// that produced it. Question text is not an identity: two tabs can ask the
// same thing concurrently, and recovery must not show one tab the other's
// answer after a dropped connection.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("run_id") == nil {
			collection.Fields.Add(&core.TextField{Name: "run_id", Max: chat.MaxRunIDRunes})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
		if err != nil {
			return nil
		}
		if field := collection.Fields.GetByName("run_id"); field != nil {
			collection.Fields.RemoveById(field.GetId())
		}
		return app.Save(collection)
	})
}
