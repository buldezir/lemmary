package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
)

// Adds custom fields: custom_fields holds the definitions, custom_field_options
// the per-user vocabulary of an option field, custom_field_values one row per
// document and field. Correspondent and document type are the two predefined
// option fields; 1730000051 moves their data over.
//
// Named functions so the test can run them twice: a managed instance re-runs
// every migration on every boot.
func init() {
	m.Register(addCustomFields, dropCustomFields)
}

func addCustomFields(app core.App) error {
	for _, create := range []func(core.App) error{
		createCustomFieldsCollection,
		createPredefinedCustomFields,
		createCustomFieldOptionsCollection,
		createCustomFieldValuesCollection,
		shareOptionsWithDocumentReaders,
	} {
		if err := create(app); err != nil {
			return err
		}
	}
	return nil
}

func createCustomFieldsCollection(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection); err == nil {
		return nil
	}
	coll := core.NewBaseCollection(models.CustomFieldsCollection)
	// A field id is a filter path segment, and PocketBase reads a segment of
	// digits only as an array index: hence the leading letter.
	if id, ok := coll.Fields.GetByName("id").(*core.TextField); ok {
		id.Pattern = "^f[a-z0-9]+$"
		id.AutogeneratePattern = "f[a-z0-9]{14}"
	}
	// Every document page renders the fields; only an admin defines them, and
	// the predefined option fields are nobody's to change.
	admin := "@request.auth." + pairedAdminField + " = true"
	coll.ListRule = new("@request.auth.id != ''")
	coll.ViewRule = new("@request.auth.id != ''")
	coll.CreateRule = new(admin + " && @request.body.type != 'option'")
	// A type change would strand the values already stored in the old type's
	// column.
	coll.UpdateRule = new(admin + " && type != 'option' && @request.body.type:changed = false")
	coll.DeleteRule = new(admin + " && type != 'option'")
	coll.Fields.Add(
		&core.TextField{Name: "name", Required: true, Max: 100},
		&core.SelectField{
			Name:      "type",
			Required:  true,
			MaxSelect: 1,
			Values: []string{
				models.CustomFieldText, models.CustomFieldNumber, models.CustomFieldDate, models.CustomFieldOption,
			},
		},
		&core.TextField{Name: "description", Max: 500},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	coll.AddIndex("idx_custom_fields_name", true, "name COLLATE NOCASE", "")
	return app.Save(coll)
}

func createPredefinedCustomFields(app core.App) error {
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		return err
	}
	for _, field := range models.PredefinedCustomFields {
		if _, err := app.FindRecordById(coll, field.ID); err == nil {
			continue
		}
		record := core.NewRecord(coll)
		record.Id = field.ID
		record.Set("name", field.Name)
		record.Set("type", field.Type)
		if err := app.Save(record); err != nil {
			return err
		}
	}
	return nil
}

func createCustomFieldOptionsCollection(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(models.CustomFieldOptionsCollection); err == nil {
		return nil
	}
	fields, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		return err
	}
	coll := core.NewBaseCollection(models.CustomFieldOptionsCollection)
	// List stays owner-only, like the collections these replace: a filter
	// dropdown must never fill up with somebody else's vocabulary. A reader of
	// a shared document still sees the owner's option on it.
	owner := "user = @request.auth.id"
	coll.ListRule = new(owner)
	coll.ViewRule = new(owner)
	coll.CreateRule = new(owner + " && field.type = 'option'")
	coll.UpdateRule = new(owner + " && @request.body.field:changed = false && @request.body.user:changed = false")
	coll.DeleteRule = new(owner)
	coll.Fields.Add(
		&core.RelationField{Name: "field", Required: true, CollectionId: fields.Id, MaxSelect: 1, CascadeDelete: true},
		&core.RelationField{Name: "user", Required: true, CollectionId: "_pb_users_auth_", MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "name", Required: true, Max: 255},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	coll.AddIndex("idx_custom_field_options_name", true, "field, `user`, name", "")
	if err := app.Save(coll); err != nil {
		return err
	}
	if err := addNgxIDField(app, models.CustomFieldOptionsCollection); err != nil {
		return err
	}
	// Per field: correspondent and document type ids were separate number
	// spaces in paperless-ngx, and clients hold both.
	return indexNgxID(app, models.CustomFieldOptionsCollection, "field")
}

func createCustomFieldValuesCollection(app core.App) error {
	if _, err := app.FindCollectionByNameOrId(models.CustomFieldValuesCollection); err == nil {
		return nil
	}
	documents, err := app.FindCollectionByNameOrId("documents")
	if err != nil {
		return err
	}
	fields, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		return err
	}
	options, err := app.FindCollectionByNameOrId(models.CustomFieldOptionsCollection)
	if err != nil {
		return err
	}
	coll := core.NewBaseCollection(models.CustomFieldValuesCollection)
	// Readable wherever the document is. No write rules: the server coerces
	// every value to its field's type and checks an option belongs to the
	// document's owner, and nothing else may write.
	readable := "document.user = @request.auth.id || (@request.auth.id != '' && document." +
		documentSharesCollection + "_via_document.user ?= @request.auth.id)"
	coll.ListRule = new(readable)
	coll.ViewRule = new(readable)
	coll.Fields.Add(
		&core.RelationField{Name: "document", Required: true, CollectionId: documents.Id, MaxSelect: 1, CascadeDelete: true},
		&core.RelationField{Name: "field", Required: true, CollectionId: fields.Id, MaxSelect: 1, CascadeDelete: true},
		&core.TextField{Name: "text", Max: models.MaxCustomFieldValueRunes},
		&core.NumberField{Name: "number"},
		&core.DateField{Name: "date"},
		&core.RelationField{Name: "option", CollectionId: options.Id, MaxSelect: 1, CascadeDelete: true},
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
	coll.AddIndex("idx_custom_field_values_document", true, "document, field", "")
	coll.AddIndex("idx_custom_field_values_text", false, "field, text", "")
	coll.AddIndex("idx_custom_field_values_number", false, "field, number", "")
	coll.AddIndex("idx_custom_field_values_date", false, "field, date", "")
	coll.AddIndex("idx_custom_field_values_option", false, "field, option", "")
	return app.Save(coll)
}

// shareOptionsWithDocumentReaders runs once the values collection exists, which
// the rule reads through.
func shareOptionsWithDocumentReaders(app core.App) error {
	coll, err := app.FindCollectionByNameOrId(models.CustomFieldOptionsCollection)
	if err != nil {
		return err
	}
	coll.ViewRule = new("user = @request.auth.id || (@request.auth.id != '' && " + models.CustomFieldValuesCollection +
		"_via_option.document." + documentSharesCollection + "_via_document.user ?= @request.auth.id)")
	return app.Save(coll)
}

func dropCustomFields(app core.App) error {
	for _, name := range []string{
		models.CustomFieldValuesCollection,
		models.CustomFieldOptionsCollection,
		models.CustomFieldsCollection,
	} {
		coll, err := app.FindCollectionByNameOrId(name)
		if err != nil {
			continue
		}
		if err := app.Delete(coll); err != nil {
			return err
		}
	}
	return nil
}
