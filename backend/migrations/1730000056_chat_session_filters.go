package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// The document filters a research chat's last turn was scoped by, so reopening
// the chat restores them. See the field comment in internal/chat/collection.go,
// the definition a fresh install uses.
func init() {
	m.Register(addChatSessionFiltersField, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("chat_sessions")
		if err != nil {
			return nil
		}
		if f := collection.Fields.GetByName("filters"); f != nil {
			collection.Fields.RemoveById(f.GetId())
		}
		return app.Save(collection)
	})
}

func addChatSessionFiltersField(app core.App) error {
	collection, err := app.FindCollectionByNameOrId("chat_sessions")
	if err != nil {
		return err
	}
	if collection.Fields.GetByName("filters") != nil {
		return nil
	}
	collection.Fields.Add(&core.JSONField{Name: "filters", MaxSize: 16000})
	return app.Save(collection)
}
