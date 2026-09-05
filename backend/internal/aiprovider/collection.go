package aiprovider

import (
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// OAuthField holds the token pair for the SDKs that sign in instead of taking a
// pasted key. Named rather than spelled inline because three packages read it:
// the record mapper here, the migration that adds it to installs that predate
// it, and the sign-in handlers that write it.
//
// Sized for a JWT id_token plus two opaque tokens with room to spare. It sits
// beside api_key in SQLite and is covered by the vault exactly as the key is;
// like the key it is never returned by the API.
const OAuthField = "oauth"

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
		&core.TextField{Name: OAuthField, Max: 8000},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	collection.AddIndex("idx_ai_providers_alias", true, "alias", "")
	if err := app.Save(collection); err != nil {
		return nil, fmt.Errorf("create %s collection: %w", CollectionName, err)
	}
	return collection, nil
}
