package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// A tag's own colour, drawn as the outline of its chips wherever they appear.
// Optional and empty by default: every tag that already exists keeps the
// neutral border it has, and only a tag someone has coloured looks different.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("tags")
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("color") == nil {
			collection.Fields.Add(&core.TextField{Name: "color", Max: 7, Pattern: `^#[0-9a-fA-F]{6}$`})
		}
		return app.Save(collection)
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId("tags")
		if err != nil {
			return nil
		}
		if field := collection.Fields.GetByName("color"); field != nil {
			collection.Fields.RemoveById(field.GetId())
		}
		return app.Save(collection)
	})
}
