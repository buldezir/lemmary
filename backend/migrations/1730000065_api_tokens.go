package migrations

import (
	"database/sql"
	"errors"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/security"
	"lemmary/backend/internal/apitoken"
)

func init() {
	m.Register(applyAPITokens, dropAPITokens)
}

func applyAPITokens(app core.App) error {
	if err := createAPITokens(app); err != nil {
		return err
	}
	// Agent and Paperless-app tokens used to be ten-year session JWTs that
	// opened every route. A new signing secret is the only way to retire them,
	// and it signs every user out once.
	users, err := app.FindCollectionByNameOrId("users")
	if err != nil {
		return err
	}
	users.AuthToken.Secret = security.RandomString(50)
	return app.Save(users)
}

func createAPITokens(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(apitoken.CollectionName); err == nil {
		return nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	tokens := core.NewBaseCollection(apitoken.CollectionName)
	tokens.Fields.Add(
		&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1, CascadeDelete: true},
		&core.SelectField{Name: "scope", Required: true, MaxSelect: 1, Values: []string{apitoken.ScopeMCP, apitoken.ScopePaperless}},
		&core.TextField{Name: "name", Max: 100},
		&core.TextField{Name: "hash", Required: true, Hidden: true},
		&core.DateField{Name: "last_used"},
		&core.AutodateField{Name: "created", OnCreate: true},
	)
	tokens.AddIndex("idx_api_tokens_hash", true, "hash", "")
	tokens.AddIndex("idx_api_tokens_user", false, "user, scope", "")
	return app.Save(tokens)
}

func dropAPITokens(app core.App) error {
	tokens, err := app.FindCollectionByNameOrId(apitoken.CollectionName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return app.Delete(tokens)
}
