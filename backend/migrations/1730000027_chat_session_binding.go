package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// The provider and model a conversation runs on, so a chat can be opened on
// something other than the global chat/search binding in Settings.
//
// Fixed for the conversation's lifetime, like mode beside it: a transcript
// produced by one model must not be replayed to another as though the answers
// in it were its own. Empty means the Settings binding, which is every session
// that already exists -- so this migration changes no behaviour by itself.
//
// Text rather than a relation to ai_providers: see the field comment in
// internal/chat/collection.go, which is the definition a fresh install uses.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("chat_sessions")
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("provider") == nil {
			collection.Fields.Add(&core.TextField{Name: "provider", Max: 15})
		}
		if collection.Fields.GetByName("model") == nil {
			collection.Fields.Add(&core.TextField{Name: "model", Max: 200})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("chat_sessions")
		if err != nil {
			return nil
		}
		for _, name := range []string{"provider", "model"} {
			if f := collection.Fields.GetByName(name); f != nil {
				collection.Fields.RemoveById(f.GetId())
			}
		}
		return app.Save(collection)
	})
}
