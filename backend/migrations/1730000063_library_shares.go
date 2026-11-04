package migrations

import (
	"database/sql"
	"errors"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"lemmary/backend/internal/models"
)

const librarySharesCollection = "library_shares"

func init() {
	m.Register(applyLibraryShares, dropLibraryShares)
}

func applyLibraryShares(app core.App) error {
	if err := createLibraryShares(app); err != nil {
		return err
	}
	return setLibraryReadRules(app, true)
}

func createLibraryShares(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(librarySharesCollection); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
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
	return nil
}

func dropLibraryShares(app core.App) error {
	if err := setLibraryReadRules(app, false); err != nil {
		return err
	}
	shares, err := app.FindCollectionByNameOrId(librarySharesCollection)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return app.Delete(shares)
}

func setLibraryReadRules(app core.App, enabled bool) error {
	valuesReadRule := "document.user = @request.auth.id || (@request.auth.id != '' && document." + documentSharesCollection + "_via_document.user ?= @request.auth.id)"
	optionsReadRule := "user = @request.auth.id || (@request.auth.id != '' && " + models.CustomFieldValuesCollection + "_via_option.document." + documentSharesCollection + "_via_document.user ?= @request.auth.id)"
	for _, entry := range []struct {
		name, base, path string
		list             bool
	}{
		{"documents", documentReadRule, "user", true},
		{"tags", namedEntityReadRule("tags"), "documents_via_tags.user", false},
		{models.CustomFieldOptionsCollection, optionsReadRule, models.CustomFieldValuesCollection + "_via_option.document.user", false},
		{models.CustomFieldValuesCollection, valuesReadRule, "document.user", true},
	} {
		coll, err := app.FindCollectionByNameOrId(entry.name)
		if err != nil {
			return err
		}
		rule := entry.base
		if enabled {
			rule = "(" + rule + ") || (@request.auth.id != '' && " + entry.path + ".library_shares_via_owner.recipient ?= @request.auth.id)"
		}
		coll.ViewRule = new(rule)
		if entry.list {
			coll.ListRule = new(rule)
		}
		if err := app.Save(coll); err != nil {
			return err
		}
	}
	return nil
}
