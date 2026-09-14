package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/chat"
)

// Research progress used to live only in the live SSE stream. Reopening a chat
// then showed the answer with no record of how it was produced. steps is the
// trail the UI already drew; incomplete is the cut-off notice that went with
// it. Both ride on the assistant row, empty on user turns.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("steps") == nil {
			collection.Fields.Add(&core.JSONField{Name: "steps", MaxSize: chat.MaxStepsJSONBytes})
		}
		if collection.Fields.GetByName("incomplete") == nil {
			collection.Fields.Add(&core.BoolField{Name: "incomplete"})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(chat.MessagesCollection)
		if err != nil {
			return nil
		}
		for _, name := range []string{"steps", "incomplete"} {
			if field := collection.Fields.GetByName(name); field != nil {
				collection.Fields.RemoveById(field.GetId())
			}
		}
		return app.Save(collection)
	})
}
