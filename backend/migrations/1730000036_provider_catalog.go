package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/aiprovider"
)

// A model's context window is what a research turn is measured against, and no
// provider's /v1/models reliably reports one. catalog names the pi.dev
// catalogue that does. Backfilled from the SDK because that is right for every
// row an installation has today; a row pointed at an OpenAI-compatible endpoint
// that is not OpenAI is the case an admin corrects by hand.
func init() {
	m.Register(func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
		if err != nil {
			return err
		}
		if collection.Fields.GetByName("catalog") == nil {
			collection.Fields.Add(&core.SelectField{
				Name:      "catalog",
				MaxSelect: 1,
				Values:    aiprovider.CatalogProviders,
			})
			if err := app.Save(collection); err != nil {
				return err
			}
		}

		records, err := app.FindAllRecords(aiprovider.CollectionName)
		if err != nil {
			return err
		}
		for _, record := range records {
			if record.GetString("catalog") != "" {
				continue
			}
			catalog := aiprovider.DefaultCatalog(record.GetString("sdk"))
			if catalog == "" {
				continue
			}
			record.Set("catalog", catalog)
			if err := app.Save(record); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		collection, err := app.FindCollectionByNameOrId(aiprovider.CollectionName)
		if err != nil {
			return nil
		}
		if field := collection.Fields.GetByName("catalog"); field != nil {
			collection.Fields.RemoveById(field.GetId())
		}
		return app.Save(collection)
	})
}
