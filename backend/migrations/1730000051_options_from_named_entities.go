package migrations

import (
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
)

// Moves correspondents and document types into the predefined option fields:
// each record becomes an option under its old id, so the search index and every
// paperless-ngx id stay valid, and each document's relation becomes a value row.
// Raw SQL rather than app.Save, which would reindex and re-embed every document
// once per row at boot.
var namedEntityFields = []struct {
	collection string
	docField   string
	fieldID    string
}{
	{"correspondents", "correspondent", models.CorrespondentFieldID},
	{"document_types", "document_type", models.DocumentTypeFieldID},
}

func init() {
	m.Register(moveNamedEntitiesToOptions, moveOptionsToNamedEntities)
}

func moveNamedEntitiesToOptions(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	var moved []*core.Collection
	for _, f := range namedEntityFields {
		coll, err := app.FindCollectionByNameOrId(f.collection)
		if err != nil {
			continue
		}
		params := dbx.Params{"field": f.fieldID}
		if _, err := app.DB().NewQuery(`INSERT INTO ` + models.CustomFieldOptionsCollection +
			` (id, field, [[user]], name, ngx_id, created, updated)
			SELECT id, {:field}, [[user]], name, COALESCE(ngx_id, 0), created, updated FROM ` + f.collection).
			Bind(params).Execute(); err != nil {
			return err
		}
		if documents.Fields.GetByName(f.docField) != nil {
			if _, err := app.DB().NewQuery(`INSERT INTO ` + models.CustomFieldValuesCollection +
				` (id, document, field, option, created, updated)
				SELECT substr(lower(hex(randomblob(8))), 1, 15), d.id, {:field}, d.` + f.docField + `, d.updated, d.updated
				FROM documents d WHERE d.` + f.docField + ` IN (SELECT id FROM ` + f.collection + `)`).
				Bind(params).Execute(); err != nil {
				return err
			}
			documents.Fields.RemoveByName(f.docField)
		}
		moved = append(moved, coll)
	}
	if len(moved) == 0 {
		return nil
	}
	if err := app.Save(documents); err != nil {
		return err
	}
	for _, coll := range moved {
		if err := app.Delete(coll); err != nil {
			return err
		}
	}
	return nil
}

// moveOptionsToNamedEntities recreates both collections as migrations 08, 09,
// 22, 46 and 47 left them and moves the rows back.
func moveOptionsToNamedEntities(app core.App) error {
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	for _, f := range namedEntityFields {
		if _, err := app.FindCollectionByNameOrId(f.collection); err == nil {
			continue
		}
		owner := "user = @request.auth.id"
		coll := core.NewBaseCollection(f.collection)
		coll.ListRule, coll.ViewRule, coll.CreateRule = new(owner), new(owner), new(owner)
		coll.UpdateRule, coll.DeleteRule = new(owner), new(owner)
		coll.Fields.Add(
			&core.TextField{Name: "name", Required: true, Max: 255},
			&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1, CascadeDelete: true},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		coll.AddIndex("idx_"+f.collection+"_user_name", true, "`user`, name", "")
		if err := app.Save(coll); err != nil {
			return err
		}
		if err := addNgxIDField(app, f.collection); err != nil {
			return err
		}
		if err := indexNgxID(app, f.collection, ""); err != nil {
			return err
		}
		documents.Fields.Add(&core.RelationField{Name: f.docField, CollectionId: coll.Id, MaxSelect: 1})
		if err := app.Save(documents); err != nil {
			return err
		}

		params := dbx.Params{"field": f.fieldID}
		for _, query := range []string{
			`INSERT INTO ` + f.collection + ` (id, [[user]], name, ngx_id, created, updated)
				SELECT id, [[user]], name, ngx_id, created, updated FROM ` + models.CustomFieldOptionsCollection + ` WHERE field = {:field}`,
			`UPDATE documents SET ` + f.docField + ` = (SELECT v.option FROM ` + models.CustomFieldValuesCollection +
				` v WHERE v.document = documents.id AND v.field = {:field})
				WHERE id IN (SELECT document FROM ` + models.CustomFieldValuesCollection + ` WHERE field = {:field})`,
			`DELETE FROM ` + models.CustomFieldValuesCollection + ` WHERE field = {:field}`,
			`DELETE FROM ` + models.CustomFieldOptionsCollection + ` WHERE field = {:field}`,
		} {
			if _, err := app.DB().NewQuery(query).Bind(params).Execute(); err != nil {
				return err
			}
		}
	}
	return setSharedReadRules(app, true)
}
