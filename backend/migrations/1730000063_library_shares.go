package migrations

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"lemmary/backend/internal/models"
)

const librarySharesCollection = "library_shares"
const libraryReaderRule = "@request.auth.id != '' && user.library_shares_via_owner.recipient ?= @request.auth.id"

func init() {
	m.Register(func(app core.App) error {
		shares := core.NewBaseCollection(librarySharesCollection)
		owner := "@request.auth.id != '' && owner = @request.auth.id"
		shares.CreateRule = new(owner + " && recipient != @request.auth.id")
		shares.ListRule = new(owner + " || (@request.auth.id != '' && recipient = @request.auth.id)")
		shares.ViewRule = shares.ListRule
		shares.DeleteRule = new(owner)
		shares.Fields.Add(
			&core.RelationField{Name: "owner", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1, CascadeDelete: true},
			&core.RelationField{Name: "recipient", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1, CascadeDelete: true},
			&core.AutodateField{Name: "created", OnCreate: true},
		)
		shares.AddIndex("idx_library_shares_pair", true, "owner, recipient", "")
		shares.AddIndex("idx_library_shares_recipient", false, "recipient", "")
		if err := app.Save(shares); err != nil {
			return err
		}
		return setLibraryReadRules(app, true)
	}, func(app core.App) error {
		if err := setLibraryReadRules(app, false); err != nil {
			return err
		}
		shares, err := app.FindCollectionByNameOrId(librarySharesCollection)
		if err != nil {
			return err
		}
		return app.Delete(shares)
	})
}

func setLibraryReadRules(app core.App, enabled bool) error {
	for _, name := range []string{"documents", "tags", models.CustomFieldOptionsCollection, models.CustomFieldValuesCollection} {
		coll, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			return err
		}
		rule := *coll.ViewRule
		libraryRule := libraryReaderRule
		if name == models.CustomFieldValuesCollection {
			libraryRule = "@request.auth.id != '' && document.user.library_shares_via_owner.recipient ?= @request.auth.id"
		}
		if enabled {
			rule = "(" + rule + ") || (" + libraryRule + ")"
		} else {
			rule = strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(rule, " || ("+libraryRule+")"), "("), ")")
		}

		coll.ViewRule = new(rule)
		if name == "documents" || name == models.CustomFieldValuesCollection {
			coll.ListRule = new(rule)
		}
		if err := app.Save(coll); err != nil {
			return err
		}
	}
	return nil
}
