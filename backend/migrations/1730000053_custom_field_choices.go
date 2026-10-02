package migrations

import (
	"slices"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"

	"lemmary/backend/internal/models"
)

// Adds the choice field type: custom_field_choices holds the admin's fixed
// values of each choice field, and a value row points at one through its new
// choice column.
func init() {
	m.Register(addCustomFieldChoices, dropCustomFieldChoices)
}

func addCustomFieldChoices(app core.App) error {
	fields, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		return err
	}
	if typeField, ok := fields.Fields.GetByName("type").(*core.SelectField); ok && !slices.Contains(typeField.Values, models.CustomFieldChoice) {
		typeField.Values = append(typeField.Values, models.CustomFieldChoice)
		if err := app.Save(fields); err != nil {
			return err
		}
	}
	choices, err := app.FindCollectionByNameOrId(models.CustomFieldChoicesCollection)
	if err != nil {
		choices = core.NewBaseCollection(models.CustomFieldChoicesCollection)
		admin := "@request.auth." + pairedAdminField + " = true"
		choices.ListRule = new("@request.auth.id != ''")
		choices.ViewRule = new("@request.auth.id != ''")
		choices.CreateRule = new(admin + " && field.type = '" + models.CustomFieldChoice + "'")
		choices.UpdateRule = new(admin + " && @request.body.field:changed = false")
		choices.DeleteRule = new(admin)
		choices.Fields.Add(
			&core.RelationField{Name: "field", Required: true, CollectionId: fields.Id, MaxSelect: 1, CascadeDelete: true},
			&core.TextField{Name: "name", Required: true, Max: models.MaxCustomFieldValueRunes},
			&core.AutodateField{Name: "created", OnCreate: true},
			&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
		)
		choices.AddIndex("idx_custom_field_choices_name", true, "field, name COLLATE NOCASE", "")
		if err := app.Save(choices); err != nil {
			return err
		}
	}
	values, err := app.FindCollectionByNameOrId(models.CustomFieldValuesCollection)
	if err != nil || values.Fields.GetByName("choice") != nil {
		return err
	}
	values.Fields.Add(&core.RelationField{Name: "choice", CollectionId: choices.Id, MaxSelect: 1, CascadeDelete: true})
	values.AddIndex("idx_custom_field_values_choice", false, "field, choice", "")
	return app.Save(values)
}

func dropCustomFieldChoices(app core.App) error {
	fields, err := app.FindCollectionByNameOrId(models.CustomFieldsCollection)
	if err != nil {
		return nil
	}
	choiceFields, err := app.FindAllRecords(fields, dbx.HashExp{"type": models.CustomFieldChoice})
	if err != nil {
		return err
	}
	for _, record := range choiceFields {
		if err := app.Delete(record); err != nil {
			return err
		}
	}
	if values, err := app.FindCollectionByNameOrId(models.CustomFieldValuesCollection); err == nil && values.Fields.GetByName("choice") != nil {
		values.Fields.RemoveByName("choice")
		values.RemoveIndex("idx_custom_field_values_choice")
		if err := app.Save(values); err != nil {
			return err
		}
	}
	if choices, err := app.FindCollectionByNameOrId(models.CustomFieldChoicesCollection); err == nil {
		if err := app.Delete(choices); err != nil {
			return err
		}
	}
	if typeField, ok := fields.Fields.GetByName("type").(*core.SelectField); ok {
		typeField.Values = slices.DeleteFunc(typeField.Values, func(v string) bool { return v == models.CustomFieldChoice })
		return app.Save(fields)
	}
	return nil
}
