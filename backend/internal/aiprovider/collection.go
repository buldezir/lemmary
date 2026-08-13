package aiprovider

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

func EnsureCollection(app core.App) (*core.Collection, error) {
	if collection, err := app.FindCollectionByNameOrId(CollectionName); err == nil {
		return collection, nil
	}

	collection := core.NewBaseCollection(CollectionName)
	collection.Fields.Add(
		&core.SelectField{
			Name:      "sdk",
			Required:  true,
			MaxSelect: 1,
			Values:    ValidSDKs,
		},
		&core.TextField{Name: "alias", Required: true, Max: 100},
		&core.TextField{Name: "base_url", Max: 500},
		&core.TextField{Name: "api_key", Max: 2000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	collection.AddIndex("idx_ai_providers_alias", true, "alias", "")
	if err := app.Save(collection); err != nil {
		return nil, fmt.Errorf("create %s collection: %w", CollectionName, err)
	}
	return collection, nil
}
